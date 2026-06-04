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
			COALESCE(SUM(tokens.t), 0) as total_tokens
		FROM loom_traces lt
		LEFT JOIN (
			SELECT trace_id, 
			       SUM(CAST(COALESCE(JSON_EXTRACT(attributes_json, '$."gen_ai.usage.input_tokens"'), 0) AS INTEGER)) +
			       SUM(CAST(COALESCE(JSON_EXTRACT(attributes_json, '$."gen_ai.usage.output_tokens"'), 0) AS INTEGER)) as t
			FROM spans
			GROUP BY trace_id
		) tokens ON lt.trace_id = tokens.trace_id
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
		ThreadID    string `json:"thread_id"`
		GraphName   string `json:"graph_name"`
		StartTime   int64  `json:"start_time"`
		TraceCount  int    `json:"trace_count"`
		TotalTokens int64  `json:"total_tokens"`
	}
	var threads []Thread
	for rows.Next() {
		var t Thread
		if err := rows.Scan(&t.ThreadID, &t.GraphName, &t.StartTime, &t.TraceCount, &t.TotalTokens); err != nil {
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

	rows, err := s.db.db.Query(`
		SELECT mp.timestamp_unix_nano, mp.value, ms.attributes_json
		FROM metric_points mp
		JOIN metric_series ms ON mp.series_id = ms.id
		WHERE ms.metric_name = ?
		ORDER BY mp.timestamp_unix_nano ASC
	`, metricName)
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
