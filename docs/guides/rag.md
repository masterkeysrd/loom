# Documents, Vector Stores & Retrievers (RAG) 📚

Loom provides a modular, end-to-end **Retrieval-Augmented Generation (RAG)** pipeline built around three focused packages:

1. **`document`** (`github.com/masterkeysrd/loom/document`) — `Document`, lazy iterator `Loader` implementations, and text `Splitter` chunkers (`RecursiveCharacterSplitter`, `MarkdownSplitter`).
2. **`vectorstore`** (`github.com/masterkeysrd/loom/vectorstore`) — `VectorStore` interface and backends (`memory`, `sqlite`, `pg`) for batch embedding, similarity search, and metadata filtering.
3. **`retriever`** (`github.com/masterkeysrd/loom/retriever`) — `Retriever` runtime interface with OpenTelemetry spans, `on_retrieve` stream events, Reciprocal Rank Fusion (`NewMerger`), System One decision reranking (`NewDecisionReranker`), and agent tool integration (`NewTool`).

```mermaid
flowchart LR
    A["Loader<br/>(FS, CSV, JSON, Reader)"] -->|iter.Seq2| B["Splitter<br/>(Recursive, Markdown)"]
    B -->|[]Document chunks| C["VectorStore<br/>(memory, sqlite, pg)"]
    D["Query"] --> E["Retriever<br/>(Vector, RRF, DecisionReranker)"]
    C <-->|Top-K + Filter| E
    E -->|[]Document| F["Graph Node / Agent Tool"]
```

---

## 1. Loading Documents (`document.Loader`)

Every loader in `document` implements a single-method lazy iterator interface using Go's `iter.Seq2[Document, error]`:

```go
type Loader interface {
    Load(ctx context.Context) iter.Seq2[Document, error]
}
```

### Built-in Loaders

| Loader | Constructor | Description |
|---|---|---|
| `TextLoader` | `document.NewTextLoader(text, meta...)` | Wraps an in-memory string as a `Document`. |
| `ReaderLoader` | `document.NewReaderLoader(r, source, meta...)` | Reads any `io.Reader` into a single `Document`. |
| `FSLoader` | `document.NewFSLoader(fsys, patterns...)` | Lazily walks an `fs.FS` (e.g. `os.DirFS`, `embed.FS`) matching glob patterns and populates `source`, `path`, `name`, `ext` metadata. |
| `CSVLoader` | `document.NewCSVLoader(r, opts...)` | Streams CSV rows lazily (`WithCSVSource`, `WithCSVContentColumns`, `WithCSVDelimiter`). |
| `JSONLoader` | `document.NewJSONLoader(r, opts...)` | Streams JSON arrays, objects, or JSONL (`WithJSONLines(true)`, `WithJSONContentKey`). |
| `HTMLLoader` | `document.NewHTMLLoader(r, opts...)` | Parses HTML DOM via `golang.org/x/net/html`, extracts `<title>` and `<meta name="description">`, and strips `<script>`/`<style>`/boilerplate tags. |

```go
import (
    "context"
    "os"

    "github.com/masterkeysrd/loom/document"
)

ctx := context.Background()

// Lazily walk all Markdown files in ./docs
loader := document.NewFSLoader(os.DirFS("./docs"), "*.md")

// Materialize into a slice when needed:
docs, err := document.LoadAll(ctx, loader)
```

---

## 2. Splitting Documents (`document.Splitter`)

Large documents should be split into smaller, overlapping chunks before embedding so that retrieved passages stay focused and fit comfortably in the model's context window.

### Recursive Character Splitter
Splits text hierarchically by `["\n\n", "\n", " ", ""]` to keep paragraphs and sentences intact:

```go
splitter := document.NewRecursiveCharacterSplitter(
    document.WithChunkSize(1000),
    document.WithChunkOverlap(200),
)

chunks := splitter.SplitDocuments(docs)
```

### Markdown Splitter
Splits Markdown by headings (`#`–`######`), records active section headings in each chunk's `Metadata` (`"h1"`, `"h2"`, etc.), and keeps fenced code blocks intact:

```go
mdSplitter := document.NewMarkdownSplitter(
    document.WithChunkSize(800),
    document.WithChunkOverlap(100),
)

// Stream lazily from Loader -> Splitter without loading all files into memory at once:
for chunk, err := range document.SplitIter(loader.Load(ctx), mdSplitter) {
    if err != nil {
        log.Fatal(err)
    }
    // chunk.Metadata["h1"], chunk.Metadata["chunk_index"], chunk.Metadata["parent_id"]
}
```

---

## 3. Indexing & Searching (`vectorstore.VectorStore`)

`vectorstore.VectorStore` embeds batches of documents via [`embedding.Model.EmbedDocuments`](./embedding.md) and searches them by cosine similarity.

### Choosing a Backend

=== "In-Memory (`vectorstore/memory`)"

    ```go
    import (
        "github.com/masterkeysrd/loom/vectorstore/memory"
    )

    vs := memory.New(embedModel)
    ```

=== "SQLite (`vectorstore/sqlite`)"

    ```go
    import (
        "database/sql"
        "github.com/masterkeysrd/loom/vectorstore/sqlite"
        _ "modernc.org/sqlite"
    )

    db, _ := sql.Open("sqlite", "vectors.db")
    vs, err := sqlite.New(db, embedModel)
    ```

=== "PostgreSQL `pgvector` (`vectorstore/pg`)"

    ```go
    import (
        "database/sql"
        _ "github.com/lib/pq"
        "github.com/masterkeysrd/loom/vectorstore/pg"
    )

    db, _ := sql.Open("postgres", os.Getenv("POSTGRES_DSN"))
    vs, err := pg.New(db, embedModel, pg.WithDims(1536))
    ```

### Adding, Searching, and Deleting Documents

```go
// 1. Batch-embed and index chunks
ids, err := vs.Add(ctx, chunks, vectorstore.WithAddNamespace("docs"))

// 2. Similarity search with metadata filter, minimum score, and optional MMR diversification
matches, err := vs.Search(ctx, "How do I resume an interrupted graph?", 5,
    vectorstore.WithNamespace("docs"),
    vectorstore.WithFilter(map[string]any{"ext": ".md"}),
    vectorstore.WithMinScore(0.55),
    vectorstore.WithMMR(0.5, 20), // Balance relevance (0.5) and diversity across top 20 candidates
)

// 3. Delete by ID
_ = vs.Delete(ctx, ids, vectorstore.WithDeleteNamespace("docs"))
```

---

## 4. Composable Retrievers (`retriever.Retriever`)

The `retriever` package provides a uniform query interface (`Retrieve(ctx, query, opts...) ([]document.Document, error)`) with built-in OpenTelemetry tracing and `on_retrieve` stream events.

### Vector Store Retriever
```go
import "github.com/masterkeysrd/loom/retriever"

baseRetriever := retriever.FromVectorStore(vs,
    retriever.WithNamespace("docs"),
    retriever.WithTopK(10),
    retriever.WithMMR(0.6, 30),
)
```

### BM25 Lexical Retriever & Reciprocal Rank Fusion (`NewBM25` + `NewMerger`)
Pair `BM25Retriever` (Okapi BM25 keyword search) with `VectorStoreRetriever` inside `NewMerger` for hybrid lexical + semantic search:

```go
bm25Retriever := retriever.NewBM25(chunks)

mergedRetriever := retriever.NewMerger(
    []retriever.Retriever{baseRetriever, bm25Retriever},
    retriever.WithRRFConstant(60),
)
```

### System One Decision Reranker (`NewDecisionReranker`)
Use a fast [`decision.Model`](./decision.md) to evaluate each candidate chunk in parallel, discard irrelevant passages below a probability threshold, and sort by calibrated relevance probability:

```go
reranker := retriever.NewDecisionReranker(
    baseRetriever,
    decisionModel,
    "Does this document contain factual information that helps answer the query?",
    retriever.WithThreshold(0.65),
)

topDocs, err := reranker.Retrieve(ctx, "How does SQLite checkpointing work?", retriever.WithTopK(3))
```

### Agent Tool Integration (`NewTool`)
Expose any `Retriever` directly to an LLM agent as a read-only [`tool.Tool`](./tools.md):

```go
kbTool, err := retriever.NewTool(
    "search_knowledge_base",
    "Search internal documentation for architecture and API usage guides",
    reranker,
    retriever.WithTopK(4),
)

container := tool.NewContainer(kbTool)
chatModel = chatModel.BindToolDefs(container.Definitions()...)
```

---

## 5. End-to-End Example (`examples/rag`)

Loom includes a complete, runnable end-to-end RAG example in [`examples/rag/main.go`](../../examples/rag/main.go). It demonstrates:
- Streaming documents lazily with `document.NewFSLoader`.
- Chunking Markdown by heading hierarchy with `document.NewMarkdownSplitter`.
- In-memory vector store indexing (`vectorstore/memory`) with dense embeddings.
- Hybrid lexical (BM25) and semantic retrieval with Reciprocal Rank Fusion (`retriever.NewMerger`).
- A stateful RAG graph workflow (`retrieve` $\to$ `generate`) using `graph.Builder`.

### Running with Ollama (Local)

Ensure Ollama is running and has the required models installed (`ollama pull gemma3` and `ollama pull nomic-embed-text`):

```bash
go run ./examples/rag -provider ollama
```

You can customize the query and models using CLI flags:

```bash
go run ./examples/rag -provider ollama -llm-model gemma3 -embed-model nomic-embed-text -query "How does Loom checkpoint graph state?"
```

### Running with OpenAI

```bash
export OPENAI_API_KEY="sk-..."
go run ./examples/rag -provider openai -query "How do decision models route graph workflows?"
```

