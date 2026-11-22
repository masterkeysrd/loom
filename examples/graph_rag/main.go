package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/masterkeysrd/loom/document"
	"github.com/masterkeysrd/loom/embedding"
	"github.com/masterkeysrd/loom/graph"
	"github.com/masterkeysrd/loom/graphstore"
	gssqlite "github.com/masterkeysrd/loom/graphstore/sqlite"
	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	loomollama "github.com/masterkeysrd/loom/providers/ollama"
	loomopenai "github.com/masterkeysrd/loom/providers/openai"
	"github.com/masterkeysrd/loom/retriever"
	vsmemory "github.com/masterkeysrd/loom/vectorstore/memory"
)

type graphRAGState struct {
	Query  string              `json:"query"`
	Docs   []document.Document `json:"docs,omitempty"`
	Answer string              `json:"answer,omitempty"`
}

func (s graphRAGState) Copy() graphRAGState {
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
	flag.StringVar(&queryFlag, "query", "Which datastore is impacted if the Checkout API triggers an Auth Service outage?", "Multi-hop GraphRAG query")
	flag.Parse()

	ctx := context.Background()

	var (
		chatModel  *llm.Model
		embedModel *embedding.Model
		err        error
	)

	switch strings.ToLower(providerFlag) {
	case "ollama":
		if llmModelFlag == "" {
			llmModelFlag = "gemma3"
		}
		if embedModelFlag == "" {
			embedModelFlag = "nomic-embed-text"
		}
		prov, err := loomollama.NewDefaultProvider()
		if err != nil {
			log.Fatalf("create ollama provider: %v", err)
		}
		chatModel, err = llm.NewModel(prov, llmModelFlag, nil)
		if err != nil {
			log.Fatalf("create chat model: %v", err)
		}
		embedModel, err = embedding.NewModel(prov, embedModelFlag, nil)
		if err != nil {
			log.Fatalf("create embedding model: %v", err)
		}
	case "openai":
		if llmModelFlag == "" {
			llmModelFlag = "gpt-4o-mini"
		}
		if embedModelFlag == "" {
			embedModelFlag = "text-embedding-3-small"
		}
		prov, err := loomopenai.NewDefaultProvider()
		if err != nil {
			log.Fatalf("create openai provider: %v", err)
		}
		chatModel, err = llm.NewModel(prov, llmModelFlag, nil)
		if err != nil {
			log.Fatalf("create chat model: %v", err)
		}
		embedModel, err = embedding.NewModel(prov, embedModelFlag, nil)
		if err != nil {
			log.Fatalf("create embedding model: %v", err)
		}
	default:
		log.Fatalf("unsupported provider %q: must be 'ollama' or 'openai'", providerFlag)
	}

	chunks := []document.Document{
		document.New("Checkout API routes every incoming order validation request to Auth Service and Billing Service.").WithID("arch-1"),
		document.New("Auth Service issues OAuth tokens and depends on Redis Cluster for low-latency session revocation.").WithID("arch-2"),
		document.New("Billing Service writes ledger transactions to PostgreSQL Primary and publishes events to Kafka.").WithID("arch-3"),
	}

	// 1. Index unstructured chunks into an in-memory vector store.
	vs := vsmemory.New(embedModel)
	if _, err := vs.Add(ctx, chunks); err != nil {
		log.Fatalf("vectorstore add: %v", err)
	}

	// 2. Extract typed entities and relationships into a SQLite knowledge graph store.
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		log.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	gs, err := gssqlite.New(db, gssqlite.WithEmbedding(embedModel))
	if err != nil {
		log.Fatalf("create sqlite graphstore: %v", err)
	}

	extractor, err := graphstore.NewExtractor(
		chatModel,
		graphstore.WithAllowedNodeTypes("Service", "Infrastructure"),
		graphstore.WithAllowedEdgeTypes("CALLS", "DEPENDS_ON", "WRITES_TO", "PUBLISHES_TO"),
		graphstore.WithConcurrency(2),
	)
	if err != nil {
		log.Fatalf("create graph extractor: %v", err)
	}

	graphDocs, err := extractor.Extract(ctx, chunks)
	if err != nil {
		log.Fatalf("extract knowledge graph: %v", err)
	}
	if err := gs.Upsert(ctx, graphDocs); err != nil {
		log.Fatalf("upsert knowledge graph: %v", err)
	}

	// 3. Compose Hybrid GraphRAG: Vector + BM25 + 2-hop Knowledge Graph via RRF.
	vecRetriever := retriever.FromVectorStore(vs, retriever.WithTopK(4))
	bm25Retriever := retriever.NewBM25(chunks)
	kgRetriever := retriever.FromGraphStore(gs,
		retriever.WithGraphStoreMaxDepth(2),
		retriever.WithGraphStoreSeedK(3),
		retriever.WithGraphStoreRenderMode(retriever.RenderHybrid),
		retriever.WithGraphStoreDocumentStore(vs),
		retriever.WithQueryEntityExtractor(chatModel),
	)

	hybrid := retriever.NewMerger(
		[]retriever.Retriever{vecRetriever, bm25Retriever, kgRetriever},
		retriever.WithRRFConstant(60),
	)

	// 4. Wire retrieve -> generate inside a Loom graph.
	b := graph.New[graphRAGState]()
	b.AddNode("retrieve", graph.NodeFunc(func(ctx context.Context, state graphRAGState) (graph.Command[graphRAGState], error) {
		docs, err := hybrid.Retrieve(ctx, state.Query, retriever.WithTopK(6))
		if err != nil {
			return nil, err
		}
		return graph.Update[graphRAGState](func(s graphRAGState) graphRAGState {
			s.Docs = docs
			return s
		}), nil
	}))
	b.AddNode("generate", graph.NodeFunc(func(ctx context.Context, state graphRAGState) (graph.Command[graphRAGState], error) {
		var sb strings.Builder
		for i, doc := range state.Docs {
			fmt.Fprintf(&sb, "[%d] (%s)\n%s\n\n", i+1, doc.ID, doc.Content)
		}
		reply, err := chatModel.Invoke(ctx, []message.Message{
			message.NewSystemText("Answer the question concisely using only the retrieved knowledge graph relationships and source passages."),
			message.NewUserText(fmt.Sprintf("Context:\n%s\nQuestion: %s", sb.String(), state.Query)),
		})
		if err != nil {
			return nil, err
		}
		answer := reply.Content.Text()
		return graph.Update[graphRAGState](func(s graphRAGState) graphRAGState {
			s.Answer = answer
			return s
		}), nil
	}))
	b.AddEdge(graph.START, "retrieve")
	b.AddEdge("retrieve", "generate")
	b.AddEdge("generate", graph.END)

	g, err := b.WithName("GraphRAGWorkflow").Build()
	if err != nil {
		log.Fatalf("build graph: %v", err)
	}

	inputCmd := graph.Update[graphRAGState](func(s graphRAGState) graphRAGState {
		s.Query = queryFlag
		return s
	})
	snap, err := g.Execute(ctx, inputCmd, nil)
	if err != nil {
		log.Fatalf("execute graph: %v", err)
	}

	fmt.Printf("Query: %s\n\nAnswer:\n%s\n", snap.State.Query, snap.State.Answer)
}
