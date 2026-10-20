package pg_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	_ "github.com/lib/pq"

	"github.com/masterkeysrd/loom/document"
	"github.com/masterkeysrd/loom/embedding"
	"github.com/masterkeysrd/loom/vectorstore"
	"github.com/masterkeysrd/loom/vectorstore/pg"
)

type fakeMigrationDB struct {
	versions map[string]int
	executed []string
}

func (f *fakeMigrationDB) Connect(context.Context) (driver.Conn, error) { return f, nil }
func (f *fakeMigrationDB) Driver() driver.Driver                        { return nil }
func (f *fakeMigrationDB) Prepare(string) (driver.Stmt, error)          { return nil, nil }
func (f *fakeMigrationDB) Close() error                                 { return nil }
func (f *fakeMigrationDB) Begin() (driver.Tx, error)                    { return nil, nil }

func (f *fakeMigrationDB) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	trimmed := strings.TrimSpace(query)
	if strings.HasPrefix(trimmed, "INSERT INTO vectorstore_migrations") {
		comp := args[0].Value.(string)
		ver := int(args[1].Value.(int64))
		f.versions[comp] = ver
		return driver.RowsAffected(1), nil
	}
	if !strings.Contains(query, "CREATE TABLE IF NOT EXISTS vectorstore_migrations") {
		f.executed = append(f.executed, trimmed)
	}
	return driver.RowsAffected(0), nil
}

func (f *fakeMigrationDB) QueryContext(_ context.Context, _ string, args []driver.NamedValue) (driver.Rows, error) {
	comp := args[0].Value.(string)
	ver, ok := f.versions[comp]
	if !ok {
		return &fakeRows{done: true}, nil
	}
	return &fakeRows{val: int64(ver)}, nil
}

type fakeRows struct {
	val  int64
	done bool
}

func (r *fakeRows) Columns() []string { return []string{"version"} }
func (r *fakeRows) Close() error      { return nil }
func (r *fakeRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	dest[0] = r.val
	return nil
}

func TestComponentVersionedMigrations(t *testing.T) {
	fake := &fakeMigrationDB{versions: make(map[string]int)}
	db := sql.OpenDB(fake)
	defer db.Close()

	// 1. Run migrations with dims = 1536
	if err := pg.Migrate(db, 1536); err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}
	if fake.versions["vectorstore"] != 3 {
		t.Fatalf("expected vectorstore version 3, got %d", fake.versions["vectorstore"])
	}
	// Steps: extension, table, ns index, hnsw index
	if len(fake.executed) != 4 {
		t.Fatalf("expected 4 executed statements, got %d: %v", len(fake.executed), fake.executed)
	}
	if !strings.Contains(fake.executed[1], "vector(1536)") {
		t.Errorf("expected vector(1536) in table creation, got %s", fake.executed[1])
	}
	if !strings.Contains(fake.executed[3], "USING hnsw (embedding vector_cosine_ops)") {
		t.Errorf("expected hnsw index, got %s", fake.executed[3])
	}

	// 2. Run again - should be idempotent and execute 0 additional statements
	fake.executed = nil
	if err := pg.Migrate(db, 1536); err != nil {
		t.Fatalf("idempotent Migrate failed: %v", err)
	}
	if len(fake.executed) != 0 {
		t.Errorf("expected 0 executed statements on second run, got %d", len(fake.executed))
	}
}

func TestFormatVector(t *testing.T) {
	v := embedding.Vector{0.1, 0.25, 0.5}
	formatted := pg.FormatVector(v)
	expected := "[0.1,0.25,0.5]"
	if formatted != expected {
		t.Errorf("FormatVector(%v) = %q; want %q", v, formatted, expected)
	}

	if empty := pg.FormatVector(nil); empty != "[]" {
		t.Errorf("expected [], got %q", empty)
	}
}

func TestParseVector(t *testing.T) {
	s := "[0.1, 0.25, 0.5]"
	parsed := pg.ParseVector(s)
	if len(parsed) != 3 || parsed[0] != 0.1 || parsed[1] != 0.25 || parsed[2] != 0.5 {
		t.Errorf("ParseVector(%q) = %v; want [0.1, 0.25, 0.5]", s, parsed)
	}

	if pg.ParseVector("") != nil {
		t.Errorf("expected nil for empty string")
	}
	if pg.ParseVector("[]") != nil {
		t.Errorf("expected nil for '[]'")
	}
	if pg.ParseVector("invalid") != nil {
		t.Errorf("expected nil for invalid vector string")
	}
}

type deterministicProvider struct{}

func (d deterministicProvider) Name() string { return "deterministic" }

func (d deterministicProvider) Embed(ctx context.Context, req *embedding.Request) (*embedding.Response, error) {
	embeddings := make([]embedding.Vector, len(req.Inputs))
	for i := range req.Inputs {
		embeddings[i] = embedding.Vector{0.1, 0.2, 0.3}
	}
	return &embedding.Response{
		Model:      req.Model,
		Embeddings: embeddings,
	}, nil
}

func TestPGStore_Integration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("skipping PostgreSQL integration test; POSTGRES_DSN not set")
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("failed to connect to postgres: %v", err)
	}
	defer db.Close()

	model, err := embedding.NewModel(deterministicProvider{}, "test-embed", nil)
	if err != nil {
		t.Fatalf("failed to create model: %v", err)
	}

	store, err := pg.New(db, model, pg.WithDims(3))
	if err != nil {
		t.Fatalf("pg.New failed: %v", err)
	}

	ctx := context.Background()
	ns := "test_" + uuid.NewString()

	doc1 := document.New("hello world", map[string]any{"lang": "en"}).WithID("doc-1")
	doc2 := document.New("bonjour le monde", map[string]any{"lang": "fr"}).WithID("doc-2")

	ids, err := store.Add(ctx, []document.Document{doc1, doc2}, vectorstore.WithAddNamespace(ns))
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 IDs, got %v", ids)
	}

	// Search
	results, err := store.Search(ctx, "hello", 5, vectorstore.WithNamespace(ns), vectorstore.WithFilter(map[string]any{"lang": "en"}))
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 1 || results[0].ID != "doc-1" {
		t.Fatalf("expected only doc-1, got %v", results)
	}

	// Delete
	if err := store.Delete(ctx, []string{"doc-1"}, vectorstore.WithDeleteNamespace(ns)); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	resultsAfter, err := store.Search(ctx, "hello", 5, vectorstore.WithNamespace(ns))
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(resultsAfter) != 1 || resultsAfter[0].ID != "doc-2" {
		t.Fatalf("expected only doc-2 remaining, got %v", resultsAfter)
	}
}

func TestPGStore_EdgeCases(t *testing.T) {
	_, err := pg.New(nil, nil)
	if err == nil {
		t.Errorf("expected error with nil db")
	}

	fake := &fakeMigrationDB{versions: make(map[string]int)}
	db := sql.OpenDB(fake)
	defer db.Close()

	model, _ := embedding.NewModel(deterministicProvider{}, "test-embed", nil)
	store, err := pg.New(db, model, pg.WithDims(3))
	if err != nil {
		t.Fatalf("pg.New failed: %v", err)
	}

	// Empty add
	ids, err := store.Add(context.Background(), nil)
	if err != nil || ids != nil {
		t.Errorf("expected nil for empty Add, got %v, %v", ids, err)
	}

	// k <= 0 search
	docs, err := store.Search(context.Background(), "query", 0)
	if err != nil || docs != nil {
		t.Errorf("expected nil for k=0, got %v, %v", docs, err)
	}

	// Empty delete
	err = store.Delete(context.Background(), nil)
	if err != nil {
		t.Errorf("expected nil for empty Delete, got %v", err)
	}
}
