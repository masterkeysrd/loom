# document

The `document` package provides the foundational primitives for loading, representing, and chunking text documents in Loom. It has zero external dependencies (relying strictly on the Go standard library) and uses Go's `iter.Seq2` iterators for lazy, memory-efficient ingestion pipelines.

---

## Core Abstractions

| Type | Description |
|---|---|
| `Document` | Canonical unit of text (`Content`), `ID`, `Metadata` (`map[string]any`), and relevance `Score` (`float32`). |
| `Loader` | Single-method interface (`Load(ctx) iter.Seq2[Document, error]`) for streaming documents lazily from any source. |
| `Splitter` | Interface (`SplitText`, `SplitDocuments`) for breaking large documents into smaller, overlapping chunks while enriching metadata. |

---

## Creating Documents

```go
import "github.com/masterkeysrd/loom/document"

doc := document.New("Loom is a graph-based AI workflow engine for Go.", map[string]any{
    "source": "intro.md",
}).
    WithID("doc-1").
    WithMetadata("category", "architecture")
```

All `With*` methods (`WithID`, `WithScore`, `WithMetadata`) return copies without mutating the original `Document` or its `Metadata` map.

---

## Document Loaders

Every loader implements the single-method `Loader` interface:

```go
type Loader interface {
    Load(ctx context.Context) iter.Seq2[Document, error]
}
```

You can iterate lazily with `for doc, err := range loader.Load(ctx)` or materialize all documents into memory with `document.LoadAll(ctx, loader)`:

### 1. `TextLoader` & `ReaderLoader`
```go
// In-memory string
textLoader := document.NewTextLoader("Hello world", map[string]any{"author": "alice"})

// Any io.Reader
readerLoader := document.NewReaderLoader(fileReader, "notes.txt")
```

### 2. `FSLoader` (`io/fs.FS` with Glob Patterns)
Lazily walks any `fs.FS` (such as `os.DirFS("./docs")` or `embed.FS`) and filters files by glob pattern:

```go
fsLoader := document.NewFSLoader(os.DirFS("./docs"), "*.md", "*.txt")

for doc, err := range fsLoader.Load(ctx) {
    if err != nil {
        log.Fatal(err)
    }
    // Automatically populates Metadata: "source", "path", "name", "ext"
    fmt.Println(doc.ID, doc.Metadata["name"])
}
```

### 3. `CSVLoader`
Streams CSV rows one by one, treating the first row as column headers:

```go
csvLoader := document.NewCSVLoader(csvFile,
    document.WithCSVSource("products.csv"),
    document.WithCSVContentColumns("title", "description"), // Other columns go to Metadata
    document.WithCSVDelimiter(','),
)
docs, err := document.LoadAll(ctx, csvLoader)
```

### 4. `JSONLoader` (JSON Arrays, Objects, and JSONL)
Streams JSON arrays (`[...]`), single JSON objects (`{...}`), or newline-delimited JSON Lines (`WithJSONLines(true)`):

```go
jsonLoader := document.NewJSONLoader(jsonlFile,
    document.WithJSONSource("articles.jsonl"),
    document.WithJSONLines(true),
    document.WithJSONContentKey("body"), // Uses "body" as Content; other fields go to Metadata
)
```

---

## Text Splitters

Splitters divide documents into chunks while preserving parent metadata and adding `"chunk_index"`, `"chunk_total"`, `"parent_id"`, and chunk IDs (`"<parent_id>#<index>"`).

### 1. `RecursiveCharacterSplitter`
Splits text hierarchically using a prioritized list of separators (default: `["\n\n", "\n", " ", ""]`) to keep paragraphs and sentences together while respecting `ChunkSize` and `ChunkOverlap`:

```go
splitter := document.NewRecursiveCharacterSplitter(
    document.WithChunkSize(1000),
    document.WithChunkOverlap(200),
)

chunks := splitter.SplitDocuments(docs)
```

### 2. `MarkdownSplitter`
Splits Markdown documents by heading sections (`#` through `######`), tracks the active heading hierarchy in each chunk's `Metadata` (`"h1"`, `"h2"`, ..., `"h6"`), and protects fenced code blocks (```` ``` ```` and `~~~`) from accidental splitting:

```go
mdSplitter := document.NewMarkdownSplitter(
    document.WithChunkSize(800),
    document.WithChunkOverlap(100),
)

chunks := mdSplitter.SplitDocuments(docs)
// chunks[i].Metadata["h1"], chunks[i].Metadata["h2"], etc.
```

---

## Lazy Streaming Pipeline (`SplitIter`)

Combine `Loader.Load(ctx)` with `document.SplitIter` to stream and chunk large datasets lazily without holding all raw documents in memory:

```go
loader := document.NewFSLoader(os.DirFS("./knowledge"), "*.md")
splitter := document.NewMarkdownSplitter(document.WithChunkSize(512))

for chunk, err := range document.SplitIter(loader.Load(ctx), splitter) {
    if err != nil {
        log.Fatal(err)
    }
    // Index chunk or batch into a VectorStore
    _ = chunk
}
```
