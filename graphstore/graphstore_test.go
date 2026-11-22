package graphstore_test

import (
	"testing"

	"github.com/masterkeysrd/loom/graphstore"
)

func TestNormalizeHelpers(t *testing.T) {
	if got := graphstore.NormalizeID("  Marie Curie (Physicist) "); got != "marie_curie_physicist" {
		t.Fatalf("NormalizeID = %q, want marie_curie_physicist", got)
	}
	if got := graphstore.NormalizeID("auth-service/v2"); got != "auth_service_v2" {
		t.Fatalf("NormalizeID = %q, want auth_service_v2", got)
	}
	if got := graphstore.NormalizeEdgeType("depends-on "); got != "DEPENDS_ON" {
		t.Fatalf("NormalizeEdgeType = %q, want DEPENDS_ON", got)
	}
}

func TestOptionsDefaults(t *testing.T) {
	up := graphstore.ApplyUpsertOptions()
	if up.Namespace != graphstore.DefaultNamespace {
		t.Fatalf("unexpected default upsert namespace: %q", up.Namespace)
	}
	tr := graphstore.ApplyTraverseOptions()
	if tr.Namespace != graphstore.DefaultNamespace || !tr.Bidirectional || tr.MaxEdges != 50 || tr.MaxNodes != 100 {
		t.Fatalf("unexpected default traverse config: %+v", tr)
	}
	sr := graphstore.ApplySearchOptions(graphstore.WithNodeTypes("Service"), graphstore.WithMinScore(0.5))
	if len(sr.NodeTypes) != 1 || sr.NodeTypes[0] != "Service" || !sr.HasMinScore || sr.MinScore != 0.5 {
		t.Fatalf("unexpected search config: %+v", sr)
	}
	dl := graphstore.ApplyDeleteOptions(graphstore.WithDeleteByDocID(true), graphstore.WithDeleteNamespace("custom"))
	if !dl.ByDocID || dl.Namespace != "custom" {
		t.Fatalf("unexpected delete config: %+v", dl)
	}
}

func TestMergeNodeAndSearchText(t *testing.T) {
	merged := graphstore.MergeNode(
		graphstore.Node{ID: "redis", Label: "Redis", Type: "Infrastructure", Properties: map[string]any{"tier": "cache"}},
		graphstore.Node{ID: "redis", Properties: map[string]any{"version": 7}},
	)
	if merged.Label != "Redis" || merged.Type != "Infrastructure" || merged.Properties["tier"] != "cache" || merged.Properties["version"] != 7 {
		t.Fatalf("unexpected merged node: %+v", merged)
	}
	txt := graphstore.NodeSearchText(merged)
	if txt == "" {
		t.Fatal("expected non-empty search text")
	}
}
