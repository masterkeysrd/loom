package studio

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

type APIServer struct {
	db *DB
}

func NewAPIServer(db *DB) *APIServer {
	return &APIServer{db: db}
}

func (s *APIServer) RegisterHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/api/stats", s.handleStats)
	mux.HandleFunc("/api/threads", s.handleThreads)
	mux.HandleFunc("/api/threads/", s.handleThreadDetail)
	mux.HandleFunc("/api/metrics", s.handleMetrics)
	mux.HandleFunc("/api/metrics/", s.handleMetricData)
}

func (s *APIServer) handleStats(w http.ResponseWriter, r *http.Request) {
	var stats struct {
		TotalThreads int64   `json:"total_threads"`
		TotalSpans   int64   `json:"total_spans"`
		TotalTokens  float64 `json:"total_tokens"`
		ErrorCount   int64   `json:"error_count"`
		LLMCallCount int64   `json:"llm_call_count"`
		P50Latency   float64 `json:"p50_latency"`
	}

	_ = s.db.db.QueryRow("SELECT COUNT(DISTINCT thread_id) FROM loom_traces").Scan(&stats.TotalThreads)
	_ = s.db.db.QueryRow("SELECT COUNT(*) FROM spans").Scan(&stats.TotalSpans)
	// For cumulative counters, we want the sum of the latest value per series
	_ = s.db.db.QueryRow(`
		SELECT COALESCE(SUM(max_val), 0) FROM (
			SELECT MAX(mp.value) as max_val 
			FROM metric_points mp
			JOIN metric_series ms ON mp.series_id = ms.id
			WHERE ms.metric_name = 'gen_ai.client.token.usage'
			GROUP BY mp.series_id
		)
	`).Scan(&stats.TotalTokens)
	_ = s.db.db.QueryRow("SELECT COUNT(*) FROM spans WHERE status_code = 'Error'").Scan(&stats.ErrorCount)

	// LLM Call Count: Spans with gen_ai.operation.name
	_ = s.db.db.QueryRow(`
		SELECT COUNT(*) FROM spans 
		WHERE JSON_EXTRACT(attributes_json, '$."gen_ai.operation.name"') IS NOT NULL
	`).Scan(&stats.LLMCallCount)

	// P50 Latency (approximate using SQLite)
	_ = s.db.db.QueryRow(`
		SELECT AVG(dur) FROM (
			SELECT (end_time_unix_nano - start_time_unix_nano) / 1000000.0 as dur
			FROM spans
			WHERE JSON_EXTRACT(attributes_json, '$."gen_ai.operation.name"') IS NOT NULL
			ORDER BY dur
			LIMIT 1
			OFFSET (SELECT COUNT(*) FROM spans WHERE JSON_EXTRACT(attributes_json, '$."gen_ai.operation.name"') IS NOT NULL) / 2
		)
	`).Scan(&stats.P50Latency)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

func (s *APIServer) handleThreads(w http.ResponseWriter, r *http.Request) {
	queryParam := r.URL.Query().Get("q")
	var rows *sql.Rows
	var err error

	sqlQuery := `
		SELECT 
			lt.thread_id, 
			lt.graph_name, 
			MIN(lt.start_time_unix_nano) as start_time, 
			COUNT(lt.trace_id) as trace_count,
			COALESCE(SUM(ts.tokens), 0) as total_tokens,
			MAX(ts.has_error) as has_error,
			COALESCE(SUM(ts.llm_calls), 0) as invocation_count
		FROM loom_traces lt
		JOIN (
			SELECT 
				trace_id,
				SUM(CAST(COALESCE(JSON_EXTRACT(attributes_json, '$."gen_ai.usage.input_tokens"'), 0) AS INTEGER)) +
				SUM(CAST(COALESCE(JSON_EXTRACT(attributes_json, '$."gen_ai.usage.output_tokens"'), 0) AS INTEGER)) as tokens,
				COUNT(CASE WHEN JSON_EXTRACT(attributes_json, '$."gen_ai.operation.name"') IS NOT NULL THEN 1 END) as llm_calls,
				MAX(CASE WHEN status_code = 'STATUS_CODE_ERROR' AND (parent_span_id IS NULL OR parent_span_id = '' OR parent_span_id = '0000000000000000') THEN 1 ELSE 0 END) as has_error
			FROM spans
			GROUP BY trace_id
		) ts ON lt.trace_id = ts.trace_id
	`

	if queryParam != "" {
		sqlQuery += " WHERE lt.thread_id LIKE ? OR lt.graph_name LIKE ?"
		sqlQuery += " GROUP BY lt.thread_id, lt.graph_name ORDER BY start_time DESC"
		rows, err = s.db.db.Query(sqlQuery, "%"+queryParam+"%", "%"+queryParam+"%")
	} else {
		sqlQuery += " GROUP BY lt.thread_id, lt.graph_name ORDER BY start_time DESC"
		rows, err = s.db.db.Query(sqlQuery)
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type Thread struct {
		ThreadID        string `json:"thread_id"`
		GraphName       string `json:"graph_name"`
		StartTime       int64  `json:"start_time"`
		TraceCount      int    `json:"trace_count"`
		TotalTokens     int64  `json:"total_tokens"`
		HasError        bool   `json:"has_error"`
		InvocationCount int    `json:"invocation_count"`
	}
	var threads []Thread
	for rows.Next() {
		var t Thread
		if err := rows.Scan(&t.ThreadID, &t.GraphName, &t.StartTime, &t.TraceCount, &t.TotalTokens, &t.HasError, &t.InvocationCount); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		threads = append(threads, t)
	}

	json.NewEncoder(w).Encode(threads)
}

func (s *APIServer) handleThreadDetail(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 {
		http.NotFound(w, r)
		return
	}
	threadID := parts[3]

	// Get all trace IDs for this thread
	rows, err := s.db.db.Query("SELECT trace_id FROM loom_traces WHERE thread_id = ?", threadID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var traceIDs []any
	var placeholders []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		traceIDs = append(traceIDs, id)
		placeholders = append(placeholders, "?")
	}

	if len(traceIDs) == 0 {
		http.NotFound(w, r)
		return
	}

	// Get all spans for these traces
	query := fmt.Sprintf(`
		SELECT trace_id, span_id, parent_span_id, name, kind, start_time_unix_nano, end_time_unix_nano, attributes_json, status_code, status_message
		FROM spans
		WHERE trace_id IN (%s)
		ORDER BY start_time_unix_nano ASC
	`, strings.Join(placeholders, ","))

	spanRows, err := s.db.db.Query(query, traceIDs...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer spanRows.Close()

	var spans []SpanRecord
	for spanRows.Next() {
		var s SpanRecord
		var attrStr string
		if err := spanRows.Scan(&s.TraceID, &s.SpanID, &s.ParentSpanID, &s.Name, &s.Kind, &s.StartTimeNano, &s.EndTimeNano, &attrStr, &s.StatusCode, &s.StatusMessage); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.Unmarshal([]byte(attrStr), &s.Attributes)
		spans = append(spans, s)
	}

	// Sort spans to ensure logical waterfall
	sort.Slice(spans, func(i, j int) bool {
		return spans[i].StartTimeNano < spans[j].StartTimeNano
	})

	json.NewEncoder(w).Encode(spans)
}

func (s *APIServer) handleMetrics(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.db.Query("SELECT name, description, unit, type FROM metrics")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var metrics []MetricRecord
	for rows.Next() {
		var m MetricRecord
		rows.Scan(&m.Name, &m.Description, &m.Unit, &m.Type)
		metrics = append(metrics, m)
	}

	json.NewEncoder(w).Encode(metrics)
}

func (s *APIServer) handleMetricData(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 {
		http.NotFound(w, r)
		return
	}
	metricName := parts[3]
	interval := r.URL.Query().Get("interval") // e.g. "10s", "1m", "5m"

	var query string
	var args []any

	if interval != "" {
		// Parse interval into seconds
		var seconds int64
		switch interval {
		case "1s":
			seconds = 1
		case "5s":
			seconds = 5
		case "10s":
			seconds = 10
		case "30s":
			seconds = 30
		case "1m":
			seconds = 60
		case "5m":
			seconds = 300
		case "15m":
			seconds = 900
		case "1h":
			seconds = 3600
		default:
			seconds = 60 // Default to 1m if unknown
		}

		bucketNano := seconds * 1e9

		// We aggregate by series AND time bucket.
		// For counters (Sum), we take the MAX in the bucket (assuming they are cumulative)
		// For gauges/histograms, we take the AVG.
		// Note: We use COALESCE/MAX/AVG logic based on the metric type.
		// Since we don't know the type for sure here without a join, we can look at the metrics table.
		query = fmt.Sprintf(`
			SELECT 
				(mp.timestamp_unix_nano / %[1]d) * %[1]d as bucket_time,
				CASE 
					WHEN m.type LIKE '%%Sum%%' THEN MAX(mp.value)
					ELSE AVG(mp.value)
				END as val,
				ms.attributes_json
			FROM metric_points mp
			JOIN metric_series ms ON mp.series_id = ms.id
			JOIN metrics m ON ms.metric_name = m.name
			WHERE ms.metric_name = ?
			GROUP BY bucket_time, ms.id
			ORDER BY bucket_time ASC
		`, bucketNano)
		args = append(args, metricName)
	} else {
		query = `
			SELECT mp.timestamp_unix_nano, mp.value, ms.attributes_json
			FROM metric_points mp
			JOIN metric_series ms ON mp.series_id = ms.id
			WHERE ms.metric_name = ?
			ORDER BY mp.timestamp_unix_nano ASC
		`
		args = append(args, metricName)
	}

	rows, err := s.db.db.Query(query, args...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var points []MetricPoint
	for rows.Next() {
		var p MetricPoint
		var attrStr string
		if err := rows.Scan(&p.TimestampNano, &p.Value, &attrStr); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		p.MetricName = metricName
		json.Unmarshal([]byte(attrStr), &p.Attributes)
		points = append(points, p)
	}

	json.NewEncoder(w).Encode(points)
}
