// Package document provides document abstractions, lazy loaders, and text splitters
// for Retrieval-Augmented Generation (RAG) and document processing workflows.
//
// The package relies strictly on the Go standard library, introducing zero
// external or internal Loom dependencies.
//
// # Core Types
//
//   - [Document]: Represents a piece of text along with an identifier, arbitrary
//     metadata key-value pairs, and an optional relevance score.
//   - [Loader]: Lazily yields [Document] items via Go 1.23+ iterators ([iter.Seq2]).
//   - [Splitter]: Decomposes text and documents into smaller chunks suitable for
//     embedding models and vector stores.
//
// # Loaders
//
// Loaders stream documents lazily without loading entire corpora into memory:
//   - [TextLoader]: Loads a single in-memory string.
//   - [ReaderLoader]: Reads content from any [io.Reader].
//   - [FSLoader]: Recursively walks a filesystem ([io/fs.FS]) matching glob patterns.
//   - [CSVLoader]: Streams tabular records row-by-row with customizable column mapping.
//   - [JSONLoader]: Decodes JSON arrays, single objects, or newline-delimited JSONL streams.
//
// Use [LoadAll] to collect all documents from a loader into a slice when lazy iteration
// is not required.
//
// # Splitters
//
// Splitters divide documents into overlapping segments while preserving context:
//   - [RecursiveCharacterSplitter]: Splits text hierarchically using a prioritized
//     list of separators (paragraphs, lines, words, characters) while respecting
//     configured chunk size and chunk overlap constraints.
//   - [MarkdownSplitter]: Understands Markdown structure, tracking active heading
//     hierarchies (h1 through h6) as chunk metadata and protecting fenced code blocks
//     from being split internally unless they exceed the chunk size limit.
//
// Use [SplitIter] to compose loaders and splitters into streaming pipelines:
//
//	loader := document.NewFSLoader(os.DirFS("./docs"), "*.md")
//	splitter := document.NewMarkdownSplitter(document.WithChunkSize(500))
//	for chunk, err := range document.SplitIter(loader.Load(ctx), splitter) {
//	    if err != nil {
//	        log.Fatal(err)
//	    }
//	    // process chunk
//	}
package document
