package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"
	"testing/fstest"

	"github.com/masterkeysrd/loom/document"
	"github.com/masterkeysrd/loom/embedding"
	"github.com/masterkeysrd/loom/graph"
	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	loomollama "github.com/masterkeysrd/loom/providers/ollama"
	loomopenai "github.com/masterkeysrd/loom/providers/openai"
	"github.com/masterkeysrd/loom/retriever"
	"github.com/masterkeysrd/loom/telemetry"
	"github.com/masterkeysrd/loom/vectorstore/memory"
)

// RAGState models the state of the retrieval-augmented generation workflow.
// It implements graph.State[RAGState].
type RAGState struct {
	Query  string              `json:"query"`
	Docs   []document.Document `json:"docs,omitempty"`
	Answer string              `json:"answer,omitempty"`
}

// Copy creates a deep copy to satisfy graph.State[RAGState].
func (s RAGState) Copy() RAGState {
	clone := s
	if s.Docs != nil {
		clone.Docs = make([]document.Document, len(s.Docs))
		for i, doc := range s.Docs {
			d := doc
			d.Metadata = document.CloneMetadata(doc.Metadata)
			clone.Docs[i] = d
		}
	}
	return clone
}

func main() {
	var (
		providerFlag   string
		llmModelFlag   string
		embedModelFlag string
		queryFlag      string
	)
	flag.StringVar(&providerFlag, "provider", "ollama", "Provider to use: ollama or openai")
	flag.StringVar(&llmModelFlag, "llm-model", "", "LLM model to use (default: gemma3 for ollama, gpt-4o-mini for openai)")
	flag.StringVar(&embedModelFlag, "embed-model", "", "Embedding model to use (default: nomic-embed-text for ollama, text-embedding-3-small for openai)")
	flag.StringVar(&queryFlag, "query", "How does Loom persist graph state and support hybrid search?", "Query to retrieve context and answer")
	flag.Parse()

	switch strings.ToLower(providerFlag) {
	case "ollama":
		if llmModelFlag == "" {
			llmModelFlag = "gemma3"
		}
		if embedModelFlag == "" {
			embedModelFlag = "nomic-embed-text"
		}
	case "openai":
		if llmModelFlag == "" {
			llmModelFlag = "gpt-4o-mini"
		}
		if embedModelFlag == "" {
			embedModelFlag = "text-embedding-3-small"
		}
	default:
		log.Fatalf("unsupported provider %q: must be 'ollama' or 'openai'", providerFlag)
	}

	// Enable content recording to see raw messages and retrieval payloads in Loom Studio
	ctx := telemetry.WithContentRecording(context.Background())

	// Initialize Telemetry
	shutdown, err := telemetry.Init(ctx, telemetry.Config{
		ServiceName: "rag-example",
		Insecure:    true,
	})
	if err != nil {
		log.Printf("Warning: failed to initialize telemetry: %v", err)
	} else {
		defer shutdown(ctx)
	}

	var (
		llmProvider   llm.Provider
		embedProvider embedding.Provider
	)

	switch strings.ToLower(providerFlag) {
	case "ollama":
		p, err := loomollama.NewDefaultProvider()
		if err != nil {
			log.Fatalf("failed to create Ollama provider: %v", err)
		}
		llmProvider = p
		embedProvider = p
	case "openai":
		p, err := loomopenai.NewDefaultProvider()
		if err != nil {
			log.Fatalf("failed to create OpenAI provider: %v", err)
		}
		llmProvider = p
		embedProvider = p
	}

	embedModel, err := embedding.NewModel(embedProvider, embedModelFlag, nil)
	if err != nil {
		log.Fatalf("failed to create embedding model: %v", err)
	}

	llmModel, err := llm.NewModel(llmProvider, llmModelFlag, nil)
	if err != nil {
		log.Fatalf("failed to create LLM model: %v", err)
	}

	fmt.Println("=================================================================")
	fmt.Printf("Loom Retrieval-Augmented Generation (RAG) Agent Example\n")
	fmt.Printf("Provider: %s | LLM: %s | Embedding: %s\n", providerFlag, llmModelFlag, embedModelFlag)
	fmt.Printf("Query: %s\n", queryFlag)
	fmt.Println("=================================================================")
	fmt.Println()

	// -----------------------------------------------------------------
	// Part 1: Sample In-Memory Knowledge Base (fstest.MapFS)
	// -----------------------------------------------------------------
	memFS := fstest.MapFS{
		"graphs.md": &fstest.MapFile{
			Data: []byte(`# Loom Stateful Graph Workflows

## Core Graph Architecture
Loom organizes agent execution into stateful directed graphs where execution flows across nodes.
Each graph is defined over a typed state struct implementing the graph.State[T] interface, which requires a Copy() T method for immutable state transitions.
State updates are dispatched via graph.Command[T], such as graph.Update[T](mutatorFn).

## Node Functions and Edges
Graph nodes are defined with graph.NodeFunc[T], taking a context.Context and the current state, and returning a graph.Command[T] and an error.
Nodes are connected via static edges using builder.AddEdge("fromNode", "toNode") or conditional routing edges.
Special entry and exit anchors graph.START and graph.END mark the graph boundaries.

## Graph Compilation and Mermaid Diagrams
Calling builder.Build() compiles the graph and validates reachability, cycle rules, and terminal edges.
The compiled graph can be visualized by generating Mermaid workflow diagrams via g.ToMermaid() and viewed in the browser using g.MermaidURL().
Execution is triggered with g.Execute(ctx, inputCmd, nil), returning a snapshot with the final state.`),
		},
		"checkpointing.md": &fstest.MapFile{
			Data: []byte(`# State Persistence and Checkpointing

## Checkpoint Architecture
Loom graphs support persistent checkpointing to persist graph state across interruptions, handle human-in-the-loop approvals, recover from crashes, and resume multi-turn conversations.
A checkpointer records state snapshots, pending commands, and step versions at each node transition.

## SQLite Checkpointer
The SQLite checkpointer (checkpoint/sqlite) provides lightweight, zero-dependency embedded state persistence.
It stores serialized state snapshots in an embedded SQLite database using pure Go drivers (modernc.org/sqlite).
To attach a checkpointer, configure builder.WithCheckpointer(cp) with a session or thread ID.

## PostgreSQL Checkpointer
For distributed and production environments, the PostgreSQL checkpointer (checkpoint/pg) persists state in Postgres tables with connection pooling and row-level locking.
This enables horizontally scaled workers to safely coordinate and resume interrupted graphs.`),
		},
		"decision.md": &fstest.MapFile{
			Data: []byte(`# System One Fast Decision Models

## Probabilistic Decision Engine
Loom provides System One probabilistic decision models (decision.Model) for ultra-fast, structured decisions without full chat completion overhead.
Decision models evaluate input state against one or more decision questions simultaneously.

## Decision Question Types
Three question types are supported:
1. decision.Predicate: Evaluates binary boolean conditions and returns a calibrated probability.
2. decision.Choice: Selects the best option from a discrete set of categories with probability distribution.
3. decision.Score: Computes expected numeric scores and level distributions across multi-tiered rubrics.

## Graph Routing Integration
Decision models act as intelligent branch routers in graph workflows.
decision.AddRouteEdge routes categorical choices to target graph nodes with confidence thresholds and fallback paths.
decision.AddConditionalEdge evaluates boolean predicates to branch conditionally or exit to graph.END.`),
		},
		"rag.md": &fstest.MapFile{
			Data: []byte(`# Retrieval-Augmented Generation and Hybrid Search

## Modular RAG Architecture
Loom provides a modular RAG pipeline across three packages: document, vectorstore, and retriever.
The ingestion pipeline streams documents lazily, splits them into semantic chunks, and indexes them for retrieval.

## Ingestion and Markdown Splitting
document.NewFSLoader lazily reads files from any fs.FS filesystem, such as embedded filesystems or testing/fstest.MapFS.
document.NewMarkdownSplitter splits markdown text while respecting heading hierarchies (# through ######), attaching active heading metadata (h1, h2) to each chunk.
document.SplitIter pipes loader streams directly into splitters without loading entire datasets into memory.

## Vector Storage and MMR
Vector stores (vectorstore/memory, vectorstore/sqlite, vectorstore/pg) store chunk embeddings generated by embedding.Model.
Maximal Marginal Relevance (MMR) search via retriever.WithMMR(lambda, fetchK) balances similarity and diversity among candidate chunks.

## Hybrid Retrieval with Reciprocal Rank Fusion
Loom supports hybrid search by combining dense semantic vector retrieval with sparse lexical BM25 retrieval.
retriever.NewBM25 indexes document chunks for Okapi BM25 keyword matching.
retriever.NewMerger combines vector and BM25 retrievers using Reciprocal Rank Fusion (RRF) with a configurable smoothing constant (retriever.WithRRFConstant(60)).
Candidate chunks are deduplicated and ranked to ensure both exact keyword matches and semantic context are retrieved.`),
		},
	}

	// -----------------------------------------------------------------
	// Part 2: Ingestion Pipeline (document + vectorstore/memory)
	// -----------------------------------------------------------------
	fmt.Println("--- Ingestion Pipeline ---")
	loader := document.NewFSLoader(memFS, "*.md")
	splitter := document.NewMarkdownSplitter(
		document.WithChunkSize(400),
		document.WithChunkOverlap(50),
	)

	var chunks []document.Document
	for chunk, err := range document.SplitIter(loader.Load(ctx), splitter) {
		if err != nil {
			log.Fatalf("failed to chunk document: %v", err)
		}
		chunks = append(chunks, chunk)
	}
	fmt.Printf("Loaded documents from in-memory filesystem and created %d chunks\n", len(chunks))

	vs := memory.New(embedModel)
	if _, err := vs.Add(ctx, chunks); err != nil {
		log.Fatalf("failed to index chunks into memory vector store: %v", err)
	}
	fmt.Println("Indexed chunks into vectorstore/memory")

	bm25Retriever := retriever.NewBM25(chunks)
	fmt.Println("Indexed chunks into retriever.BM25")
	fmt.Println()

	// -----------------------------------------------------------------
	// Part 3: Hybrid Retriever (retriever)
	// -----------------------------------------------------------------
	vecRetriever := retriever.FromVectorStore(vs, retriever.WithTopK(4), retriever.WithMMR(0.6, 12))
	hybridRetriever := retriever.NewMerger(
		[]retriever.Retriever{vecRetriever, bm25Retriever},
		retriever.WithRRFConstant(60),
	)
	fmt.Println("Configured hybrid retriever (Vector MMR + BM25 merged via RRF)")
	fmt.Println()

	// -----------------------------------------------------------------
	// Part 4: Stateful Graph Workflow (graph)
	// -----------------------------------------------------------------
	builder := graph.New[RAGState]()

	// Node 1: "retrieve"
	builder.AddNode("retrieve", graph.NodeFunc[RAGState](func(ctx context.Context, state RAGState) (graph.Command[RAGState], error) {
		fmt.Printf("\n[Node: retrieve] Querying hybrid retriever for: %q\n", state.Query)
		docs, err := hybridRetriever.Retrieve(ctx, state.Query, retriever.WithTopK(3))
		if err != nil {
			return nil, fmt.Errorf("retrieval failed: %w", err)
		}

		fmt.Printf("Retrieved %d relevant chunk(s):\n", len(docs))
		for i, doc := range docs {
			heading := "General"
			if h, ok := doc.Metadata["h2"].(string); ok && h != "" {
				heading = h
			} else if h, ok := doc.Metadata["h1"].(string); ok && h != "" {
				heading = h
			}
			fmt.Printf("  [%d] ID: %-18s | Score: %.4f | Heading: %q\n", i+1, doc.ID, doc.Score, heading)
		}

		return graph.Update[RAGState](func(s RAGState) RAGState {
			s.Docs = docs
			return s
		}), nil
	}))

	// Node 2: "generate"
	builder.AddNode("generate", graph.NodeFunc[RAGState](func(ctx context.Context, state RAGState) (graph.Command[RAGState], error) {
		fmt.Println("\n[Node: generate] Formatting context and invoking LLM...")
		var contextBuilder strings.Builder
		for i, doc := range state.Docs {
			heading := ""
			if h, ok := doc.Metadata["h2"].(string); ok && h != "" {
				heading = fmt.Sprintf(" - %s", h)
			} else if h, ok := doc.Metadata["h1"].(string); ok && h != "" {
				heading = fmt.Sprintf(" - %s", h)
			}
			contextBuilder.WriteString(fmt.Sprintf("--- Document %d [%s%s] ---\n", i+1, doc.ID, heading))
			contextBuilder.WriteString(doc.Content)
			contextBuilder.WriteString("\n\n")
		}

		sysPrompt := "You are a knowledgeable assistant specializing in the Loom AI framework.\n" +
			"Answer the user's question using the provided context passages. Be accurate, clear, and concise."
		userPrompt := fmt.Sprintf("Context:\n%s\nQuestion: %s\n\nAnswer:", contextBuilder.String(), state.Query)

		messages := []message.Message{
			message.NewSystemText(sysPrompt),
			message.NewUserText(userPrompt),
		}

		resp, err := llmModel.Invoke(ctx, messages)
		if err != nil {
			return nil, fmt.Errorf("llm generation failed: %w", err)
		}

		answer := resp.GetContent().Text()
		return graph.Update[RAGState](func(s RAGState) RAGState {
			s.Answer = answer
			return s
		}), nil
	}))

	// Edges: START -> "retrieve" -> "generate" -> END
	builder.AddEdge(graph.START, "retrieve")
	builder.AddEdge("retrieve", "generate")
	builder.AddEdge("generate", graph.END)

	ragGraph, err := builder.WithName("RAGWorkflow").Build()
	if err != nil {
		log.Fatalf("failed to build graph: %v", err)
	}

	fmt.Println("--- Mermaid Workflow Diagram ---")
	fmt.Println(ragGraph.ToMermaid())
	fmt.Println("\n--- View Diagram in Browser ---")
	fmt.Println(ragGraph.MermaidURL())
	fmt.Println("--------------------------------")

	// Execute graph
	fmt.Println("\n--- Executing Graph Workflow ---")
	inputCmd := graph.Update[RAGState](func(s RAGState) RAGState {
		s.Query = queryFlag
		return s
	})

	snap, err := ragGraph.Execute(ctx, inputCmd, nil)
	if err != nil {
		log.Fatalf("graph execution failed: %v", err)
	}

	fmt.Println("\n=================================================================")
	fmt.Println("FINAL ANSWER")
	fmt.Println("=================================================================")
	fmt.Println(snap.State.Answer)
	fmt.Println("=================================================================")
}
