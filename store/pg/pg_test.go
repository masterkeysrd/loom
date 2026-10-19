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
	"github.com/masterkeysrd/loom/embedding"
	"github.com/masterkeysrd/loom/store"
	"github.com/masterkeysrd/loom/store/pg"
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
	if strings.HasPrefix(strings.TrimSpace(query), "INSERT INTO store_migrations") {
		comp := args[0].Value.(string)
		ver := int(args[1].Value.(int64))
		f.versions[comp] = ver
		return driver.RowsAffected(1), nil
	}
	if !strings.Contains(query, "CREATE TABLE IF NOT EXISTS store_migrations") {
		f.executed = append(f.executed, strings.TrimSpace(query))
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

	// 1. Run core migrations
	if err := pg.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if fake.versions["core"] != 1 {
		t.Fatalf("expected core version 1, got %d", fake.versions["core"])
	}
	if len(fake.executed) != 2 {
		t.Fatalf("expected 2 core statements executed, got %d", len(fake.executed))
	}

	// 2. Re-running core migrations is a no-op
	if err := pg.Migrate(db); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	if len(fake.executed) != 2 {
		t.Fatalf("expected no additional statements on second Migrate, got %d", len(fake.executed))
	}

	// 3. Enabling vector indexing later migrates the "vector" component independently
	if err := pg.MigrateVector(db, 1536); err != nil {
		t.Fatalf("MigrateVector: %v", err)
	}
	if fake.versions["vector"] != 3 {
		t.Fatalf("expected vector version 3, got %d", fake.versions["vector"])
	}
	if len(fake.executed) != 6 {
		t.Fatalf("expected 2 core + 4 vector = 6 statements, got %d", len(fake.executed))
	}
	if !strings.Contains(fake.executed[3], "vector(1536)") {
		t.Fatalf("expected vector(1536) in store_embeddings DDL, got: %s", fake.executed[3])
	}

	// 4. Re-running vector migrations is a no-op
	if err := pg.MigrateVector(db, 1536); err != nil {
		t.Fatalf("second MigrateVector: %v", err)
	}
	if len(fake.executed) != 6 {
		t.Fatalf("expected no additional statements on second MigrateVector, got %d", len(fake.executed))
	}
}

type mockEmbedProvider struct {
	fn func(text string) embedding.Vector
}

func (m *mockEmbedProvider) Name() string { return "mock" }

func (m *mockEmbedProvider) Embed(_ context.Context, req *embedding.Request) (*embedding.Response, error) {
	vecs := make([]embedding.Vector, len(req.Inputs))
	for i, in := range req.Inputs {
		vecs[i] = m.fn(in.Text)
	}
	return &embedding.Response{Model: req.Model, Embeddings: vecs}, nil
}

type testItem struct {
	Name  string   `json:"name"`
	Value int      `json:"value"`
	Tags  []string `json:"tags,omitempty"`
}

func TestFormatVector(t *testing.T) {
	if got := pg.FormatVector(nil); got != "[]" {
		t.Fatalf("expected [], got %q", got)
	}
	if got := pg.FormatVector(embedding.Vector{1.5, -0.25, 0}); got != "[1.5,-0.25,0]" {
		t.Fatalf("expected [1.5,-0.25,0], got %q", got)
	}
}

func openPostgresTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN not set")
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestStoreCRUDAndSearch(t *testing.T) {
	db := openPostgresTestDB(t)
	s, err := pg.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	ns := "test-ns-" + uuid.New().String()

	// 1. Put and Get
	item := testItem{Name: "alpha", Value: 10, Tags: []string{"a", "b"}}
	if err := s.Put(ctx, ns, "k1", item); err != nil {
		t.Fatalf("Put k1: %v", err)
	}

	var got testItem
	if err := s.Get(ctx, ns, "k1", &got); err != nil {
		t.Fatalf("Get k1: %v", err)
	}
	if got.Name != "alpha" || got.Value != 10 || len(got.Tags) != 2 {
		t.Fatalf("unexpected item: %+v", got)
	}

	// 2. Search with prefix, limit, offset
	_ = s.Put(ctx, ns, "k2", testItem{Name: "beta", Value: 20})
	_ = s.Put(ctx, ns, "other", testItem{Name: "gamma", Value: 30})

	var results []testItem
	if err := s.Search(ctx, ns, &results, store.WithKeyPrefix("k")); err != nil {
		t.Fatalf("Search prefix: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// 3. Delete
	if err := s.Delete(ctx, ns, "k1"); err != nil {
		t.Fatalf("Delete k1: %v", err)
	}
	if err := s.Get(ctx, ns, "k1", &got); err != store.ErrNotFound {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestStoreSemanticSearch(t *testing.T) {
	db := openPostgresTestDB(t)

	provider := &mockEmbedProvider{
		fn: func(text string) embedding.Vector {
			switch text {
			case "Python tutorial for agents", "programming guides":
				return embedding.Vector{1, 0, 0}
			case "Go concurrency patterns":
				return embedding.Vector{0.8, 0.6, 0}
			default:
				return embedding.Vector{0, 1, 0}
			}
		},
	}
	embedModel, err := embedding.NewModel(provider, "mock-embed", nil)
	if err != nil {
		t.Fatal(err)
	}

	s, err := pg.NewStore(db, pg.WithIndex(store.IndexConfig{
		Embed:  embedModel,
		Dims:   3,
		Fields: []string{"name"},
	}))
	if err != nil {
		t.Skipf("skipping pgvector semantic search test (pgvector may not be installed): %v", err)
	}

	ctx := context.Background()
	ns := "vec-ns-" + uuid.New().String()

	_ = s.Put(ctx, ns, "doc1", testItem{Name: "Sourdough bread recipe"})
	_ = s.Put(ctx, ns, "doc2", testItem{Name: "Python tutorial for agents"})
	_ = s.Put(ctx, ns, "doc3", testItem{Name: "Go concurrency patterns"})

	var results []testItem
	if err := s.Search(ctx, ns, &results, store.WithQuery("programming guides"), store.WithLimit(2)); err != nil {
		t.Fatalf("Search with query: %v", err)
	}
	if len(results) != 2 || results[0].Name != "Python tutorial for agents" {
		t.Fatalf("unexpected semantic search results: %+v", results)
	}
}
