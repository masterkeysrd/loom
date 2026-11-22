package graphstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/masterkeysrd/loom/document"
	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
)

const defaultExtractionSystemPrompt = `You are a precise knowledge graph extraction engine.
Extract entities (nodes) and directed relationships (edges) explicitly supported by the provided text.
- Use concise canonical lowercase snake_case identifiers for node "id" (e.g., "marie_curie", "auth_service") and preserve the human-readable name in "label".
- Use uppercase SNAKE_CASE predicates for edge "type" (e.g., "DEPENDS_ON", "CREATED", "WORKS_AT").
- Every edge "source" and "target" must match a node "id" from "nodes".
- Do not invent facts or entities not grounded in the text.`

// ExtractorOption configures a schema-guided [Extractor].
type ExtractorOption func(*Extractor)

// WithAllowedNodeTypes constrains extracted node types via JSON Schema enum
// constraints and post-extraction ontology validation.
func WithAllowedNodeTypes(types ...string) ExtractorOption {
	return func(e *Extractor) {
		e.allowedNodeTypes = cleanStrings(types)
	}
}

// WithAllowedEdgeTypes constrains extracted edge types via JSON Schema enum
// constraints and post-extraction ontology validation.
func WithAllowedEdgeTypes(types ...string) ExtractorOption {
	return func(e *Extractor) {
		var out []string
		for _, t := range types {
			if norm := NormalizeEdgeType(t); norm != "" {
				out = append(out, norm)
			}
		}
		e.allowedEdgeTypes = out
	}
}

// WithAllowedTriples constrains extracted edges to match at least one (SourceType, EdgeType, TargetType) rule.
// Empty fields in a [TripleRule] act as wildcards.
func WithAllowedTriples(rules ...TripleRule) ExtractorOption {
	return func(e *Extractor) {
		e.allowedTriples = append([]TripleRule(nil), rules...)
	}
}

// WithConcurrency sets the maximum number of concurrent chunk extractions (default: 4).
func WithConcurrency(n int) ExtractorOption {
	return func(e *Extractor) {
		if n > 0 {
			e.concurrency = n
		}
	}
}

// WithExtractionPrompt overrides the default system instruction sent during graph extraction.
func WithExtractionPrompt(prompt string) ExtractorOption {
	return func(e *Extractor) {
		if strings.TrimSpace(prompt) != "" {
			e.systemPrompt = prompt
		}
	}
}

// Extractor converts unstructured [document.Document] chunks into typed [GraphDocument]s
// using [llm.Model.WithStructuredOutput] and dynamically compiled ontology schemas.
type Extractor struct {
	model            *llm.Model
	schema           *jsonschema.Schema
	systemPrompt     string
	allowedNodeTypes []string
	allowedEdgeTypes []string
	allowedTriples   []TripleRule
	concurrency      int
}

// NewExtractor constructs a schema-guided [Extractor] backed by model.
func NewExtractor(model *llm.Model, opts ...ExtractorOption) (*Extractor, error) {
	if model == nil {
		return nil, errors.New("graphstore: extractor model cannot be nil")
	}

	e := &Extractor{
		systemPrompt: defaultExtractionSystemPrompt,
		concurrency:  4,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(e)
		}
	}

	e.schema = buildExtractionSchema(e.allowedNodeTypes, e.allowedEdgeTypes)
	e.model = model.WithStructuredOutput(e.schema)
	return e, nil
}

// Schema returns the compiled JSON Schema used for structured graph extraction.
func (e *Extractor) Schema() *jsonschema.Schema {
	if e == nil {
		return nil
	}
	return e.schema
}

// Extract converts each document chunk into a [GraphDocument] concurrently while
// preserving input slice ordering and respecting context cancellation.
func (e *Extractor) Extract(ctx context.Context, docs []document.Document) ([]GraphDocument, error) {
	if len(docs) == 0 {
		return nil, nil
	}
	if e == nil || e.model == nil {
		return nil, errors.New("graphstore: extractor is not initialized")
	}

	results := make([]GraphDocument, len(docs))
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	workers := max(min(e.concurrency, len(docs)), 1)

	jobs := make(chan int, len(docs))
	for i := range docs {
		jobs <- i
	}
	close(jobs)

	var (
		wg       sync.WaitGroup
		errMu    sync.Mutex
		firstErr error
	)

	for range workers {
		wg.Go(func() {
			for idx := range jobs {
				if ctx.Err() != nil {
					return
				}
				gd, err := e.ExtractOne(ctx, docs[idx])
				if err != nil {
					errMu.Lock()
					if firstErr == nil {
						firstErr = fmt.Errorf("graphstore: extract document %d (%s): %w", idx, docs[idx].ID, err)
						cancel()
					}
					errMu.Unlock()
					return
				}
				results[idx] = gd
			}
		})
	}

	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

// ExtractOne extracts a single [GraphDocument] from doc.
func (e *Extractor) ExtractOne(ctx context.Context, doc document.Document) (GraphDocument, error) {
	if e == nil || e.model == nil {
		return GraphDocument{}, errors.New("graphstore: extractor is not initialized")
	}
	if strings.TrimSpace(doc.Content) == "" {
		return GraphDocument{Source: doc}, nil
	}

	sysPrompt := e.buildSystemPrompt()
	reply, err := e.model.Invoke(ctx, []message.Message{
		message.NewSystemText(sysPrompt),
		message.NewUserText(doc.Content),
	})
	if err != nil {
		return GraphDocument{}, err
	}

	raw := stripCodeFence(reply.Content.Text())
	var payload extractedPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return GraphDocument{}, fmt.Errorf("decode structured graph output: %w", err)
	}

	return e.normalizeAndValidate(doc, payload), nil
}

type extractedPayload struct {
	Nodes []struct {
		ID    string `json:"id"`
		Label string `json:"label"`
		Type  string `json:"type"`
	} `json:"nodes"`
	Edges []struct {
		Source string  `json:"source"`
		Target string  `json:"target"`
		Type   string  `json:"type"`
		Weight float32 `json:"weight,omitempty"`
	} `json:"edges"`
}

func (e *Extractor) normalizeAndValidate(doc document.Document, payload extractedPayload) GraphDocument {
	allowedNodes := makeLowerSet(e.allowedNodeTypes)
	allowedEdges := makeUpperEdgeSet(e.allowedEdgeTypes)

	nodeMap := make(map[string]Node, len(payload.Nodes))
	for _, raw := range payload.Nodes {
		id := NormalizeID(raw.ID)
		if id == "" {
			id = NormalizeID(raw.Label)
		}
		if id == "" {
			continue
		}
		nodeType := strings.TrimSpace(raw.Type)
		if len(allowedNodes) > 0 {
			canonicalType, ok := allowedNodes[strings.ToLower(nodeType)]
			if !ok {
				continue
			}
			nodeType = canonicalType
		}
		label := strings.TrimSpace(raw.Label)
		if label == "" {
			label = id
		}
		n := Node{
			ID:    id,
			Label: label,
			Type:  nodeType,
		}
		if prev, ok := nodeMap[id]; ok {
			nodeMap[id] = MergeNode(prev, n)
		} else {
			nodeMap[id] = n
		}
	}

	type edgeDedup struct {
		src, dst, typ string
	}
	seenEdges := make(map[edgeDedup]struct{}, len(payload.Edges))
	edges := make([]Edge, 0, len(payload.Edges))

	for _, raw := range payload.Edges {
		src := NormalizeID(raw.Source)
		dst := NormalizeID(raw.Target)
		typ := NormalizeEdgeType(raw.Type)
		if src == "" || dst == "" || typ == "" {
			continue
		}
		if len(allowedEdges) > 0 {
			if _, ok := allowedEdges[typ]; !ok {
				continue
			}
		}

		srcNode, srcOK := nodeMap[src]
		dstNode, dstOK := nodeMap[dst]
		if !srcOK {
			if len(allowedNodes) > 0 {
				continue
			}
			srcNode = Node{ID: src, Label: src}
			nodeMap[src] = srcNode
		}
		if !dstOK {
			if len(allowedNodes) > 0 {
				continue
			}
			dstNode = Node{ID: dst, Label: dst}
			nodeMap[dst] = dstNode
		}

		if len(e.allowedTriples) > 0 && !matchesAnyTripleRule(srcNode.Type, typ, dstNode.Type, e.allowedTriples) {
			continue
		}

		key := edgeDedup{src: src, dst: dst, typ: typ}
		if _, dup := seenEdges[key]; dup {
			continue
		}
		seenEdges[key] = struct{}{}

		weight := raw.Weight
		if weight <= 0 {
			weight = 1.0
		}
		edges = append(edges, Edge{
			Source: src,
			Target: dst,
			Type:   typ,
			Weight: weight,
			DocID:  doc.ID,
		})
	}

	nodeIDs := slices.Sorted(maps.Keys(nodeMap))
	nodes := make([]Node, len(nodeIDs))
	for i, id := range nodeIDs {
		nodes[i] = nodeMap[id]
	}

	return GraphDocument{
		Source: doc,
		Nodes:  nodes,
		Edges:  edges,
	}
}

func (e *Extractor) buildSystemPrompt() string {
	var sb strings.Builder
	sb.WriteString(e.systemPrompt)
	if len(e.allowedNodeTypes) > 0 {
		sb.WriteString("\nAllowed node types: ")
		sb.WriteString(strings.Join(e.allowedNodeTypes, ", "))
	}
	if len(e.allowedEdgeTypes) > 0 {
		sb.WriteString("\nAllowed edge types: ")
		sb.WriteString(strings.Join(e.allowedEdgeTypes, ", "))
	}
	if len(e.allowedTriples) > 0 {
		sb.WriteString("\nAllowed (SourceType, EdgeType, TargetType) triples:")
		for _, r := range e.allowedTriples {
			src := r.SourceType
			if src == "" {
				src = "*"
			}
			edge := r.EdgeType
			if edge == "" {
				edge = "*"
			}
			dst := r.TargetType
			if dst == "" {
				dst = "*"
			}
			fmt.Fprintf(&sb, "\n- (%s, %s, %s)", src, edge, dst)
		}
	}
	return sb.String()
}

func buildExtractionSchema(allowedNodeTypes, allowedEdgeTypes []string) *jsonschema.Schema {
	nodeTypeSchema := &jsonschema.Schema{
		Type:        "string",
		Description: "Entity class or category",
	}
	if len(allowedNodeTypes) > 0 {
		enums := make([]any, len(allowedNodeTypes))
		for i, t := range allowedNodeTypes {
			enums[i] = t
		}
		nodeTypeSchema.Enum = enums
	}

	edgeTypeSchema := &jsonschema.Schema{
		Type:        "string",
		Description: "Uppercase SNAKE_CASE relationship predicate",
	}
	if len(allowedEdgeTypes) > 0 {
		enums := make([]any, len(allowedEdgeTypes))
		for i, t := range allowedEdgeTypes {
			enums[i] = t
		}
		edgeTypeSchema.Enum = enums
	}

	noExtra := &jsonschema.Schema{Not: &jsonschema.Schema{}}

	return &jsonschema.Schema{
		Title:                "KnowledgeGraphExtraction",
		Description:          "Extracted knowledge graph nodes and directed typed relationships",
		Type:                 "object",
		AdditionalProperties: noExtra,
		Required:             []string{"nodes", "edges"},
		Properties: map[string]*jsonschema.Schema{
			"nodes": {
				Type: "array",
				Items: &jsonschema.Schema{
					Type:                 "object",
					AdditionalProperties: noExtra,
					Required:             []string{"id", "label", "type"},
					Properties: map[string]*jsonschema.Schema{
						"id":    {Type: "string", Description: "Canonical lowercase snake_case entity ID"},
						"label": {Type: "string", Description: "Human-readable entity display name"},
						"type":  nodeTypeSchema,
					},
				},
			},
			"edges": {
				Type: "array",
				Items: &jsonschema.Schema{
					Type:                 "object",
					AdditionalProperties: noExtra,
					Required:             []string{"source", "target", "type"},
					Properties: map[string]*jsonschema.Schema{
						"source": {Type: "string", Description: "Source node ID"},
						"target": {Type: "string", Description: "Target node ID"},
						"type":   edgeTypeSchema,
					},
				},
			},
		},
	}
}

func matchesAnyTripleRule(srcType, edgeType, dstType string, rules []TripleRule) bool {
	for _, r := range rules {
		if r.SourceType != "" && !strings.EqualFold(r.SourceType, srcType) {
			continue
		}
		if r.EdgeType != "" && NormalizeEdgeType(r.EdgeType) != edgeType {
			continue
		}
		if r.TargetType != "" && !strings.EqualFold(r.TargetType, dstType) {
			continue
		}
		return true
	}
	return false
}

func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	if nl := strings.IndexByte(s, '\n'); nl >= 0 {
		s = s[nl+1:]
	}
	s = strings.TrimSpace(s)
	if before, ok := strings.CutSuffix(s, "```"); ok {
		s = before
	}
	return strings.TrimSpace(s)
}

func cleanStrings(in []string) []string {
	var out []string
	seen := make(map[string]struct{}, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		key := strings.ToLower(s)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, s)
	}
	return out
}

func makeLowerSet(in []string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	m := make(map[string]string, len(in))
	for _, s := range in {
		m[strings.ToLower(s)] = s
	}
	return m
}

func makeUpperEdgeSet(in []string) map[string]struct{} {
	if len(in) == 0 {
		return nil
	}
	m := make(map[string]struct{}, len(in))
	for _, s := range in {
		if norm := NormalizeEdgeType(s); norm != "" {
			m[norm] = struct{}{}
		}
	}
	return m
}
