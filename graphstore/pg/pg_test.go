package pg

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
	"github.com/masterkeysrd/loom/graphstore"
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
	if strings.HasPrefix(trimmed, "INSERT INTO graphstore_migrations") {
		comp := args[0].Value.(string)
		ver := int(args[1].Value.(int64))
		f.versions[comp] = ver
		return driver.RowsAffected(1), nil
	}
	if !strings.Contains(query, "CREATE TABLE IF NOT EXISTS graphstore_migrations") {
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
	dest[0] = r.val
	r.done = true
	return nil
}

func TestMigrate_BaseAndOptionalVector(t *testing.T) {
	fake := &fakeMigrationDB{versions: make(map[string]int)}
	db := sql.OpenDB(fake)
	defer db.Close()

	// Base migrations only (no pgvector).
	if err := Migrate(db, false, 0); err != nil {
		t.Fatalf("Migrate base: %v", err)
	}
	joined := strings.Join(fake.executed, "\n")
	if !strings.Contains(joined, "CREATE EXTENSION IF NOT EXISTS pg_trgm") {
		t.Fatalf("expected pg_trgm extension in base migrations: %s", joined)
	}
	if strings.Contains(joined, "CREATE EXTENSION IF NOT EXISTS vector") {
		t.Fatalf("unexpected pgvector extension when vector disabled: %s", joined)
	}

	// Second call is idempotent.
	countBefore := len(fake.executed)
	if err := Migrate(db, false, 0); err != nil {
		t.Fatalf("Migrate idempotent: %v", err)
	}
	if len(fake.executed) != countBefore {
		t.Fatalf("expected idempotent migration, executed %d extra statements", len(fake.executed)-countBefore)
	}

	// Enabling vector with dims adds pgvector + HNSW.
	if err := Migrate(db, true, 1536); err != nil {
		t.Fatalf("Migrate vector: %v", err)
	}
	vecJoined := strings.Join(fake.executed[countBefore:], "\n")
	if !strings.Contains(vecJoined, "CREATE EXTENSION IF NOT EXISTS vector") ||
		!strings.Contains(vecJoined, "vector(1536)") ||
		!strings.Contains(vecJoined, "USING hnsw") {
		t.Fatalf("expected pgvector + HNSW statements, got: %s", vecJoined)
	}
}

func TestBuildTraverseSQL_PG14SearchAndCycle(t *testing.T) {
	q, args := buildTraverseSQL("default", []string{"auth_service"}, []string{"DEPENDS_ON"}, true, 2, 50)
	if !strings.Contains(q, "SEARCH BREADTH FIRST") || !strings.Contains(q, "CYCLE curr_node SET is_cycle USING path") {
		t.Fatalf("expected PG 14+ SEARCH/CYCLE clauses, got:\n%s", q)
	}
	if !strings.Contains(q, "e.type = ANY($6)") || len(args) != 6 {
		t.Fatalf("expected edge type filter and 6 args, got %d args in:\n%s", len(args), q)
	}
}

func TestPGStore_Integration(t *testing.T) {
	dsn := os.Getenv("LOOM_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("LOOM_TEST_PG_DSN not set; skipping live PostgreSQL graphstore integration test")
	}

	ctx := context.Background()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	gs, err := New(db)
	if err != nil {
		t.Fatalf("pg.New: %v", err)
	}

	ns := "test-" + uuid.NewString()
	doc := graphstore.GraphDocument{
		Source: document.New("Checkout calls Auth which depends on Redis.").WithID("doc-pg-1"),
		Nodes: []graphstore.Node{
			{ID: "checkout_api", Label: "Checkout API", Type: "Service"},
			{ID: "auth_service", Label: "Auth Service", Type: "Service"},
			{ID: "redis_cluster", Label: "Redis Cluster", Type: "Infrastructure"},
		},
		Edges: []graphstore.Edge{
			{Source: "checkout_api", Target: "auth_service", Type: "CALLS"},
			{Source: "auth_service", Target: "redis_cluster", Type: "DEPENDS_ON"},
		},
	}

	if err := gs.Upsert(ctx, []graphstore.GraphDocument{doc}, graphstore.WithUpsertNamespace(ns)); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	defer gs.Delete(ctx, []string{"checkout_api", "auth_service", "redis_cluster"}, graphstore.WithDeleteNamespace(ns))

	nodes, err := gs.SearchNodes(ctx, "Auth", 5, graphstore.WithNamespace(ns))
	if err != nil || len(nodes) == 0 || nodes[0].ID != "auth_service" {
		t.Fatalf("SearchNodes = %#v, %v", nodes, err)
	}

	sub, err := gs.Traverse(ctx, []string{"auth_service"}, 1, graphstore.WithTraverseNamespace(ns))
	if err != nil || len(sub.Nodes) != 3 || len(sub.Edges) != 2 {
		t.Fatalf("Traverse = %+v, %v", sub, err)
	}
}
