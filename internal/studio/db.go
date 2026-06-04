package studio

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type DB struct {
	db *sql.DB
}

func OpenDB(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}

	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}

	return &DB{db: db}, nil
}

func (d *DB) Close() error {
	return d.db.Close()
}

func migrate(db *sql.DB) error {
	queries := []string{
		`PRAGMA journal_mode=WAL;`,
		`CREATE TABLE IF NOT EXISTS spans (
			trace_id TEXT NOT NULL,
			span_id TEXT NOT NULL,
			parent_span_id TEXT,
			name TEXT NOT NULL,
			kind TEXT NOT NULL,
			start_time_unix_nano INTEGER NOT NULL,
			end_time_unix_nano INTEGER NOT NULL,
			attributes_json TEXT,
			status_code TEXT,
			status_message TEXT,
			PRIMARY KEY (trace_id, span_id)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_spans_trace_id ON spans(trace_id);`,
		`CREATE INDEX IF NOT EXISTS idx_spans_start_time ON spans(start_time_unix_nano);`,
		
		`CREATE TABLE IF NOT EXISTS metrics (
			name TEXT PRIMARY KEY,
			description TEXT,
			unit TEXT,
			type TEXT
		);`,
		
		`CREATE TABLE IF NOT EXISTS metric_points (
			metric_name TEXT NOT NULL,
			timestamp_unix_nano INTEGER NOT NULL,
			value REAL NOT NULL,
			attributes_json TEXT,
			FOREIGN KEY(metric_name) REFERENCES metrics(name)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_metric_points_name_time ON metric_points(metric_name, timestamp_unix_nano);`,

		// Helper table to index Loom-specific logical entities for faster queries
		`CREATE TABLE IF NOT EXISTS loom_traces (
			trace_id TEXT PRIMARY KEY,
			thread_id TEXT,
			graph_name TEXT,
			start_time_unix_nano INTEGER NOT NULL,
			FOREIGN KEY(trace_id) REFERENCES spans(trace_id)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_loom_traces_thread_id ON loom_traces(thread_id);`,
	}

	for _, q := range queries {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("migration failed: %w (query: %s)", err, q)
		}
	}
	return nil
}

type SpanRecord struct {
	TraceID        string `json:"trace_id"`
	SpanID         string `json:"span_id"`
	ParentSpanID   string `json:"parent_span_id,omitempty"`
	Name           string `json:"name"`
	Kind           string `json:"kind"`
	StartTimeNano  int64  `json:"start_time_nano"`
	EndTimeNano    int64  `json:"end_time_nano"`
	Attributes     map[string]any `json:"attributes"`
	StatusCode     string `json:"status_code"`
	StatusMessage  string `json:"status_message,omitempty"`
}

func (d *DB) InsertSpan(ctx context.Context, s SpanRecord) error {
	attrBytes, err := json.Marshal(s.Attributes)
	if err != nil {
		return err
	}

	_, err = d.db.ExecContext(ctx, `
		INSERT INTO spans (trace_id, span_id, parent_span_id, name, kind, start_time_unix_nano, end_time_unix_nano, attributes_json, status_code, status_message)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(trace_id, span_id) DO UPDATE SET
			end_time_unix_nano = excluded.end_time_unix_nano,
			attributes_json = excluded.attributes_json,
			status_code = excluded.status_code,
			status_message = excluded.status_message
	`, s.TraceID, s.SpanID, s.ParentSpanID, s.Name, s.Kind, s.StartTimeNano, s.EndTimeNano, string(attrBytes), s.StatusCode, s.StatusMessage)
	if err != nil {
		return err
	}

	// Index Loom-specific attributes
	if threadID, ok := s.Attributes["loom.thread_id"].(string); ok {
		graphName, _ := s.Attributes["loom.graph.name"].(string)
		_, _ = d.db.ExecContext(ctx, `
			INSERT INTO loom_traces (trace_id, thread_id, graph_name, start_time_unix_nano)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(trace_id) DO UPDATE SET
				thread_id = excluded.thread_id,
				graph_name = excluded.graph_name
		`, s.TraceID, threadID, graphName, s.StartTimeNano)
	}

	return nil
}

type MetricRecord struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Unit        string `json:"unit"`
	Type        string `json:"type"`
}

type MetricPoint struct {
	MetricName    string `json:"metric_name"`
	TimestampNano int64  `json:"timestamp_nano"`
	Value         float64 `json:"value"`
	Attributes    map[string]any `json:"attributes"`
}

func (d *DB) InsertMetric(ctx context.Context, m MetricRecord) error {
	_, err := d.db.ExecContext(ctx, `
		INSERT INTO metrics (name, description, unit, type)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			description = excluded.description,
			unit = excluded.unit,
			type = excluded.type
	`, m.Name, m.Description, m.Unit, m.Type)
	return err
}

func (d *DB) InsertMetricPoint(ctx context.Context, p MetricPoint) error {
	attrBytes, err := json.Marshal(p.Attributes)
	if err != nil {
		return err
	}

	_, err = d.db.ExecContext(ctx, `
		INSERT INTO metric_points (metric_name, timestamp_unix_nano, value, attributes_json)
		VALUES (?, ?, ?, ?)
	`, p.MetricName, p.TimestampNano, p.Value, string(attrBytes))
	return err
}
