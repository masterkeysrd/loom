package studio

import (
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
	mux.HandleFunc("/api/threads", s.handleThreads)
	mux.HandleFunc("/api/threads/", s.handleThreadDetail)
	mux.HandleFunc("/api/metrics", s.handleMetrics)
	mux.HandleFunc("/api/metrics/", s.handleMetricData)
}

func (s *APIServer) handleThreads(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.db.Query(`
		SELECT thread_id, graph_name, MIN(start_time_unix_nano) as start_time, COUNT(trace_id) as trace_count
		FROM loom_traces
		GROUP BY thread_id, graph_name
		ORDER BY start_time DESC
	`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type Thread struct {
		ThreadID   string `json:"thread_id"`
		GraphName  string `json:"graph_name"`
		StartTime  int64  `json:"start_time"`
		TraceCount int    `json:"trace_count"`
	}
	var threads []Thread
	for rows.Next() {
		var t Thread
		if err := rows.Scan(&t.ThreadID, &t.GraphName, &t.StartTime, &t.TraceCount); err != nil {
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
		SELECT timestamp_unix_nano, value, attributes_json
		FROM metric_points
		WHERE metric_name = ?
		ORDER BY timestamp_unix_nano ASC
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
