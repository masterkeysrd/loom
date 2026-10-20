package document_test

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strings"
	"testing"
	"testing/fstest"
	"testing/iotest"
	"unicode/utf8"

	"github.com/masterkeysrd/loom/document"
)

// -------------------------------------------------------------------------
// 1. Document Creation & Immutability Tests
// -------------------------------------------------------------------------

func TestDocument_CreationAndImmutability(t *testing.T) {
	// Simple New
	d1 := document.New("hello world")
	if d1.Content != "hello world" {
		t.Fatalf("expected content 'hello world', got %q", d1.Content)
	}
	if d1.Metadata != nil {
		t.Fatalf("expected nil metadata, got %v", d1.Metadata)
	}
	if d1.ID != "" || d1.Score != 0 {
		t.Fatalf("expected empty ID and 0 score, got ID=%q Score=%f", d1.ID, d1.Score)
	}

	// New with metadata maps
	meta1 := map[string]any{"source": "test.txt", "author": "Alice"}
	meta2 := map[string]any{"author": "Bob", "version": 2}
	d2 := document.New("doc content", meta1, meta2)
	if d2.Metadata["source"] != "test.txt" {
		t.Errorf("expected source test.txt, got %v", d2.Metadata["source"])
	}
	if d2.Metadata["author"] != "Bob" { // meta2 overwrites meta1
		t.Errorf("expected author Bob, got %v", d2.Metadata["author"])
	}
	if d2.Metadata["version"] != 2 {
		t.Errorf("expected version 2, got %v", d2.Metadata["version"])
	}

	// Mutating original map does not mutate Document
	meta1["source"] = "mutated.txt"
	if d2.Metadata["source"] != "test.txt" {
		t.Errorf("expected immutable metadata, got %v", d2.Metadata["source"])
	}

	// WithID
	d3 := d2.WithID("doc-123")
	if d3.ID != "doc-123" {
		t.Errorf("expected ID 'doc-123', got %q", d3.ID)
	}
	if d2.ID != "" {
		t.Errorf("original document ID should remain empty, got %q", d2.ID)
	}

	// WithScore
	d4 := d3.WithScore(0.95)
	if d4.Score != 0.95 {
		t.Errorf("expected score 0.95, got %f", d4.Score)
	}
	if d3.Score != 0 {
		t.Errorf("original document score should remain 0, got %f", d3.Score)
	}

	// WithMetadata
	d5 := d4.WithMetadata("tag", "golang")
	if d5.Metadata["tag"] != "golang" {
		t.Errorf("expected tag 'golang', got %v", d5.Metadata["tag"])
	}
	if _, exists := d4.Metadata["tag"]; exists {
		t.Errorf("d4 metadata should not have 'tag'")
	}

	// WithMetadata on document with nil metadata
	dEmpty := document.New("empty")
	dWithMeta := dEmpty.WithMetadata("k", "v")
	if dWithMeta.Metadata["k"] != "v" {
		t.Errorf("expected 'v', got %v", dWithMeta.Metadata["k"])
	}
	if dEmpty.Metadata != nil {
		t.Errorf("dEmpty metadata should remain nil")
	}

	// CloneMetadata
	if document.CloneMetadata(nil) != nil {
		t.Errorf("expected nil for CloneMetadata(nil)")
	}
	cloned := document.CloneMetadata(d5.Metadata)
	cloned["tag"] = "python"
	if d5.Metadata["tag"] != "golang" {
		t.Errorf("expected clone to be independent, but original was modified")
	}
}

// -------------------------------------------------------------------------
// 2. Loaders: TextLoader, ReaderLoader, FSLoader
// -------------------------------------------------------------------------

func TestTextLoader(t *testing.T) {
	ctx := context.Background()
	meta := map[string]any{"source": "mem"}
	loader := document.NewTextLoader("sample text", meta)

	docs, err := document.LoadAll(ctx, loader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 document, got %d", len(docs))
	}
	if docs[0].Content != "sample text" {
		t.Errorf("expected 'sample text', got %q", docs[0].Content)
	}
	if docs[0].Metadata["source"] != "mem" {
		t.Errorf("expected source 'mem', got %v", docs[0].Metadata["source"])
	}

	// Context cancellation
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	_, err = document.LoadAll(canceledCtx, loader)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestReaderLoader(t *testing.T) {
	ctx := context.Background()
	r := strings.NewReader("reader data")
	loader := document.NewReaderLoader(r, "stream-1", map[string]any{"format": "text"})

	docs, err := document.LoadAll(ctx, loader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 document, got %d", len(docs))
	}
	if docs[0].Content != "reader data" {
		t.Errorf("expected 'reader data', got %q", docs[0].Content)
	}
	if docs[0].Metadata["source"] != "stream-1" {
		t.Errorf("expected source 'stream-1', got %v", docs[0].Metadata["source"])
	}
	if docs[0].Metadata["format"] != "text" {
		t.Errorf("expected format 'text', got %v", docs[0].Metadata["format"])
	}

	// Reader error
	errReader := iotest.ErrReader(errors.New("read error"))
	errLoader := document.NewReaderLoader(errReader, "")
	_, err = document.LoadAll(ctx, errLoader)
	if err == nil || !strings.Contains(err.Error(), "read error") {
		t.Fatalf("expected read error, got %v", err)
	}

	// Context cancellation
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	_, err = document.LoadAll(canceledCtx, loader)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestFSLoader(t *testing.T) {
	fsys := fstest.MapFS{
		"root.txt":        {Data: []byte("root file")},
		"docs/intro.md":   {Data: []byte("# Intro")},
		"docs/guide.txt":  {Data: []byte("guide content")},
		"data/items.json": {Data: []byte("[1, 2, 3]")},
	}

	ctx := context.Background()

	// Load all files (no patterns)
	lAll := document.NewFSLoader(fsys)
	allDocs, err := document.LoadAll(ctx, lAll)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(allDocs) != 4 {
		t.Fatalf("expected 4 files, got %d", len(allDocs))
	}

	// Filter with *.txt (matches root.txt and docs/guide.txt via base name)
	lTxt := document.NewFSLoader(fsys, "*.txt")
	txtDocs, err := document.LoadAll(ctx, lTxt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(txtDocs) != 2 {
		t.Fatalf("expected 2 txt files, got %d", len(txtDocs))
	}

	// Check metadata and ID for root.txt
	var rootDoc *document.Document
	for _, d := range txtDocs {
		if d.ID == "root.txt" {
			rootDoc = &d
			break
		}
	}
	if rootDoc == nil {
		t.Fatalf("root.txt not found in loaded documents")
	}
	if rootDoc.Content != "root file" {
		t.Errorf("expected 'root file', got %q", rootDoc.Content)
	}
	if rootDoc.Metadata["source"] != "root.txt" || rootDoc.Metadata["path"] != "root.txt" {
		t.Errorf("unexpected metadata path/source: %v", rootDoc.Metadata)
	}
	if rootDoc.Metadata["name"] != "root.txt" || rootDoc.Metadata["ext"] != ".txt" {
		t.Errorf("unexpected metadata name/ext: %v", rootDoc.Metadata)
	}

	// Filter with path pattern "docs/*"
	lDocs := document.NewFSLoader(fsys, "docs/*")
	dDocs, err := document.LoadAll(ctx, lDocs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(dDocs) != 2 {
		t.Fatalf("expected 2 docs under docs/*, got %d", len(dDocs))
	}

	// Early break in consumer loop
	count := 0
	for _, err := range lAll.Load(ctx) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		count++
		if count == 1 {
			break
		}
	}
	if count != 1 {
		t.Errorf("expected early break after 1 document, got %d", count)
	}

	// Context cancellation
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	_, err = document.LoadAll(canceledCtx, lAll)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

// -------------------------------------------------------------------------
// 3. Loaders: CSVLoader
// -------------------------------------------------------------------------

func TestCSVLoader_WithoutContentColumns(t *testing.T) {
	csvData := `name,age,city
Alice,30,Wonderland
Bob,25,Builder`

	loader := document.NewCSVLoader(
		strings.NewReader(csvData),
		document.WithCSVSource("people.csv"),
	)

	ctx := context.Background()
	docs, err := document.LoadAll(ctx, loader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 documents, got %d", len(docs))
	}

	// Check row 1
	d1 := docs[0]
	expectedContent1 := "name: Alice\nage: 30\ncity: Wonderland"
	if d1.Content != expectedContent1 {
		t.Errorf("expected content:\n%q\ngot:\n%q", expectedContent1, d1.Content)
	}
	if d1.Metadata["row"] != 1 {
		t.Errorf("expected row 1, got %v", d1.Metadata["row"])
	}
	if d1.Metadata["source"] != "people.csv" {
		t.Errorf("expected source people.csv, got %v", d1.Metadata["source"])
	}
	if d1.Metadata["name"] != "Alice" || d1.Metadata["age"] != "30" || d1.Metadata["city"] != "Wonderland" {
		t.Errorf("unexpected metadata fields: %v", d1.Metadata)
	}

	// Check row 2
	d2 := docs[1]
	if d2.Metadata["row"] != 2 {
		t.Errorf("expected row 2, got %v", d2.Metadata["row"])
	}
}

func TestCSVLoader_WithContentColumns(t *testing.T) {
	csvData := `id;title;body;author
1;First Post;Hello World;Alice
2;Second Post;Go standard library;Bob`

	// Single content column
	loaderSingle := document.NewCSVLoader(
		strings.NewReader(csvData),
		document.WithCSVDelimiter(';'),
		document.WithCSVSource("posts.csv"),
		document.WithCSVContentColumns("body"),
	)

	ctx := context.Background()
	docs, err := document.LoadAll(ctx, loaderSingle)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 documents, got %d", len(docs))
	}

	if docs[0].Content != "Hello World" {
		t.Errorf("expected content 'Hello World', got %q", docs[0].Content)
	}
	if docs[0].Metadata["row"] != 1 {
		t.Errorf("expected row 1, got %v", docs[0].Metadata["row"])
	}
	if docs[0].Metadata["id"] != "1" || docs[0].Metadata["title"] != "First Post" || docs[0].Metadata["author"] != "Alice" {
		t.Errorf("remaining columns should be in metadata: %v", docs[0].Metadata)
	}
	if _, exists := docs[0].Metadata["body"]; exists {
		t.Errorf("content column 'body' should NOT be in metadata")
	}

	// Multiple content columns
	loaderMulti := document.NewCSVLoader(
		strings.NewReader(csvData),
		document.WithCSVDelimiter(';'),
		document.WithCSVContentColumns("title", "body"),
	)
	docsMulti, err := document.LoadAll(ctx, loaderMulti)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expectedMultiContent := "title: First Post\nbody: Hello World"
	if docsMulti[0].Content != expectedMultiContent {
		t.Errorf("expected multi content:\n%q\ngot:\n%q", expectedMultiContent, docsMulti[0].Content)
	}
	if docsMulti[0].Metadata["id"] != "1" || docsMulti[0].Metadata["author"] != "Alice" {
		t.Errorf("remaining columns should be in metadata: %v", docsMulti[0].Metadata)
	}
}

// -------------------------------------------------------------------------
// 4. Loaders: JSONLoader
// -------------------------------------------------------------------------

func TestJSONLoader(t *testing.T) {
	ctx := context.Background()

	// 1. JSON Array with ContentKey
	arrayJSON := `[
		{"id": 101, "text": "chunk 1", "category": "tech"},
		{"id": 102, "text": "chunk 2", "category": "news"}
	]`
	lArray := document.NewJSONLoader(
		strings.NewReader(arrayJSON),
		document.WithJSONSource("items.json"),
		document.WithJSONContentKey("text"),
	)
	docs, err := document.LoadAll(ctx, lArray)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 documents, got %d", len(docs))
	}
	if docs[0].Content != "chunk 1" {
		t.Errorf("expected 'chunk 1', got %q", docs[0].Content)
	}
	if docs[0].Metadata["source"] != "items.json" || docs[0].Metadata["category"] != "tech" {
		t.Errorf("unexpected metadata: %v", docs[0].Metadata)
	}
	if _, exists := docs[0].Metadata["text"]; exists {
		t.Errorf("content key 'text' should not be in metadata")
	}

	// 2. Single JSON Object with ContentKey
	singleJSON := `{"id": 999, "body": "single document text", "author": "dev"}`
	lSingle := document.NewJSONLoader(
		strings.NewReader(singleJSON),
		document.WithJSONContentKey("body"),
	)
	docsSingle, err := document.LoadAll(ctx, lSingle)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docsSingle) != 1 {
		t.Fatalf("expected 1 document, got %d", len(docsSingle))
	}
	if docsSingle[0].Content != "single document text" {
		t.Errorf("expected 'single document text', got %q", docsSingle[0].Content)
	}
	if docsSingle[0].Metadata["author"] != "dev" {
		t.Errorf("expected author 'dev', got %v", docsSingle[0].Metadata["author"])
	}

	// 3. Single JSON Object without ContentKey (marshaled as JSON)
	lRaw := document.NewJSONLoader(strings.NewReader(`{"status":"ok","code":200}`))
	docsRaw, err := document.LoadAll(ctx, lRaw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docsRaw) != 1 {
		t.Fatalf("expected 1 document, got %d", len(docsRaw))
	}
	if !strings.Contains(docsRaw[0].Content, `"status":"ok"`) {
		t.Errorf("expected raw JSON content, got %q", docsRaw[0].Content)
	}

	// 4. JSONLines
	jsonlData := "{\"message\": \"line 1\", \"seq\": 1}\n\n{\"message\": \"line 2\", \"seq\": 2}\n"
	lJSONL := document.NewJSONLoader(
		strings.NewReader(jsonlData),
		document.WithJSONLines(true),
		document.WithJSONContentKey("message"),
		document.WithJSONSource("logs.jsonl"),
	)
	docsJSONL, err := document.LoadAll(ctx, lJSONL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docsJSONL) != 2 {
		t.Fatalf("expected 2 documents from JSONL, got %d", len(docsJSONL))
	}
	if docsJSONL[0].Content != "line 1" || docsJSONL[1].Content != "line 2" {
		t.Errorf("unexpected JSONL contents: %q, %q", docsJSONL[0].Content, docsJSONL[1].Content)
	}
	if docsJSONL[0].Metadata["source"] != "logs.jsonl" {
		t.Errorf("expected source logs.jsonl, got %v", docsJSONL[0].Metadata["source"])
	}

	// 5. Context cancellation with JSONL
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	lCanceled := document.NewJSONLoader(strings.NewReader(jsonlData), document.WithJSONLines(true))
	_, err = document.LoadAll(canceledCtx, lCanceled)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

// -------------------------------------------------------------------------
// 5. RecursiveCharacterSplitter Tests
// -------------------------------------------------------------------------

func TestRecursiveCharacterSplitter(t *testing.T) {
	// Chunk size, overlap, separators
	text := "Paragraph one with some text.\n\nParagraph two with more details.\n\nParagraph three with conclusion."
	s := document.NewRecursiveCharacterSplitter(
		document.WithChunkSize(40),
		document.WithChunkOverlap(10),
	)

	chunks := s.SplitText(text)
	if len(chunks) == 0 {
		t.Fatalf("expected chunks, got none")
	}

	for i, chunk := range chunks {
		runeCount := utf8.RuneCountInString(chunk)
		if runeCount > 40 {
			t.Errorf("chunk %d exceeds ChunkSize (40): %d runes (%q)", i, runeCount, chunk)
		}
	}

	// Custom LengthFunc (word count)
	wordCountFn := func(s string) int {
		return len(strings.Fields(s))
	}
	sWords := document.NewRecursiveCharacterSplitter(
		document.WithChunkSize(5),
		document.WithChunkOverlap(2),
		document.WithLengthFunc(wordCountFn),
	)
	wordText := "one two three four five six seven eight nine ten"
	wordChunks := sWords.SplitText(wordText)
	if len(wordChunks) == 0 {
		t.Fatalf("expected word chunks, got none")
	}
	for i, chunk := range wordChunks {
		wc := wordCountFn(chunk)
		if wc > 5 {
			t.Errorf("chunk %d exceeds word count limit (5): %d words (%q)", i, wc, chunk)
		}
	}

	// UTF-8 runes (multi-byte characters and emojis)
	utf8Text := "你好世界，这是一个测试！🌍🚀✨ Go 语言文档切分测试。"
	sUTF8 := document.NewRecursiveCharacterSplitter(
		document.WithChunkSize(10),
		document.WithChunkOverlap(2),
	)
	utf8Chunks := sUTF8.SplitText(utf8Text)
	for i, chunk := range utf8Chunks {
		if !utf8.ValidString(chunk) {
			t.Errorf("chunk %d is not valid UTF-8: %q", i, chunk)
		}
		if utf8.RuneCountInString(chunk) > 10 {
			t.Errorf("chunk %d exceeds rune limit: %d runes (%q)", i, utf8.RuneCountInString(chunk), chunk)
		}
	}

	// SplitDocuments metadata propagation
	doc := document.New(text, map[string]any{"source": "test.txt", "author": "Loom"}).
		WithID("doc-alpha").
		WithScore(0.85)

	splitDocs := s.SplitDocuments([]document.Document{doc})
	if len(splitDocs) != len(chunks) {
		t.Fatalf("expected %d split docs, got %d", len(chunks), len(splitDocs))
	}

	for i, sd := range splitDocs {
		if sd.Metadata["chunk_index"] != i {
			t.Errorf("chunk %d has invalid chunk_index: %v", i, sd.Metadata["chunk_index"])
		}
		if sd.Metadata["chunk_total"] != len(splitDocs) {
			t.Errorf("chunk %d has invalid chunk_total: %v", i, sd.Metadata["chunk_total"])
		}
		if sd.Metadata["parent_id"] != "doc-alpha" {
			t.Errorf("chunk %d has invalid parent_id: %v", i, sd.Metadata["parent_id"])
		}
		expectedID := fmt.Sprintf("doc-alpha#%d", i)
		if sd.ID != expectedID {
			t.Errorf("chunk %d has invalid ID %q, expected %q", i, sd.ID, expectedID)
		}
		if sd.Metadata["source"] != "test.txt" || sd.Metadata["author"] != "Loom" {
			t.Errorf("chunk %d did not inherit parent metadata: %v", i, sd.Metadata)
		}
		if sd.Score != 0.85 {
			t.Errorf("chunk %d did not inherit parent score: %f", i, sd.Score)
		}
	}
}

// -------------------------------------------------------------------------
// 6. MarkdownSplitter Tests
// -------------------------------------------------------------------------

func TestMarkdownSplitter(t *testing.T) {
	mdText := `# Title

Preamble text under Title.

## Section 1

Section 1 content.

### Subsection 1.1

Deep nested details.

## Section 2

Here is a fenced code block:
` + "```python\n# This is a comment, not a markdown heading!\ndef add(a, b):\n    # Return sum\n    return a + b\n```" + `

And another code block with tildes:
` + "~~~bash\n# Bash comment\necho 'hello'\n~~~" + `

Final notes in Section 2.`

	ms := document.NewMarkdownSplitter(
		document.WithChunkSize(300),
		document.WithChunkOverlap(50),
	)

	chunks := ms.SplitText(mdText)
	if len(chunks) == 0 {
		t.Fatalf("expected markdown chunks, got none")
	}

	// Test SplitDocuments heading hierarchy metadata
	doc := document.New(mdText, map[string]any{"file": "guide.md"}).WithID("guide")
	docs := ms.SplitDocuments([]document.Document{doc})
	if len(docs) == 0 {
		t.Fatalf("expected split documents, got none")
	}

	// Check headings across documents
	var foundTitle, foundSub11, foundSec2 bool
	for _, d := range docs {
		// Verify # comments from python / bash did NOT become headings
		for k, v := range d.Metadata {
			if strings.HasPrefix(k, "h") {
				headingStr, _ := v.(string)
				if strings.Contains(headingStr, "comment") {
					t.Errorf("comment inside code block was mistakenly treated as heading %s: %q", k, headingStr)
				}
			}
		}

		if d.Metadata["h1"] == "Title" && d.Metadata["h2"] == nil {
			foundTitle = true
		}
		if d.Metadata["h1"] == "Title" && d.Metadata["h2"] == "Section 1" && d.Metadata["h3"] == "Subsection 1.1" {
			foundSub11 = true
		}
		if d.Metadata["h1"] == "Title" && d.Metadata["h2"] == "Section 2" {
			foundSec2 = true
			if d.Metadata["h3"] != nil {
				t.Errorf("Section 2 should have cleared h3, but found h3=%v", d.Metadata["h3"])
			}
		}
	}

	if !foundTitle {
		t.Errorf("expected chunk under h1 Title")
	}
	if !foundSub11 {
		t.Errorf("expected chunk under h3 Subsection 1.1")
	}
	if !foundSec2 {
		t.Errorf("expected chunk under h2 Section 2")
	}

	// Verify fenced code block was not split when fitting within ChunkSize
	foundIntactPythonBlock := false
	for _, d := range docs {
		if strings.Contains(d.Content, "def add(a, b):") && strings.Contains(d.Content, "```python") && strings.Contains(d.Content, "```") {
			foundIntactPythonBlock = true
			break
		}
	}
	if !foundIntactPythonBlock {
		t.Errorf("expected code block to remain intact within a chunk")
	}

	// Verify chunk indexing metadata
	for i, d := range docs {
		if d.Metadata["chunk_index"] != i || d.Metadata["chunk_total"] != len(docs) {
			t.Errorf("invalid chunk index/total on chunk %d", i)
		}
		if d.Metadata["parent_id"] != "guide" || d.ID != fmt.Sprintf("guide#%d", i) {
			t.Errorf("invalid parent_id or ID on chunk %d: ID=%q parent_id=%v", i, d.ID, d.Metadata["parent_id"])
		}
	}
}

func TestMarkdownSplitter_OversizedSection(t *testing.T) {
	// A section whose content is much larger than ChunkSize (100)
	hugeSection := "# Chapter 1\n\n"
	for i := 0; i < 20; i++ {
		hugeSection += fmt.Sprintf("Paragraph %d contains some repetitive sentence text for testing. ", i)
	}

	ms := document.NewMarkdownSplitter(
		document.WithChunkSize(100),
		document.WithChunkOverlap(20),
	)

	doc := document.New(hugeSection).WithID("huge")
	docs := ms.SplitDocuments([]document.Document{doc})
	if len(docs) <= 1 {
		t.Fatalf("expected multiple chunks for oversized section, got %d", len(docs))
	}

	for i, d := range docs {
		if d.Metadata["h1"] != "Chapter 1" {
			t.Errorf("chunk %d missing h1 metadata: %v", i, d.Metadata)
		}
		runeCount := utf8.RuneCountInString(d.Content)
		if runeCount > 100 {
			t.Errorf("chunk %d exceeds ChunkSize (100): %d runes", i, runeCount)
		}
	}
}

// -------------------------------------------------------------------------
// 7. SplitIter Streaming Pipeline Tests
// -------------------------------------------------------------------------

func TestSplitIter_Pipeline(t *testing.T) {
	fsys := fstest.MapFS{
		"doc1.md": {Data: []byte("# Doc 1\n\nFirst document content goes here.")},
		"doc2.md": {Data: []byte("# Doc 2\n\nSecond document content goes here.")},
	}

	ctx := context.Background()
	loader := document.NewFSLoader(fsys, "*.md")
	splitter := document.NewMarkdownSplitter(document.WithChunkSize(50))

	// Compose streaming pipeline: loader -> SplitIter -> consumer
	stream := document.SplitIter(loader.Load(ctx), splitter)

	var chunks []document.Document
	for chunk, err := range stream {
		if err != nil {
			t.Fatalf("unexpected error in stream: %v", err)
		}
		chunks = append(chunks, chunk)
	}

	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks from pipeline, got %d", len(chunks))
	}

	// Test early break in consumer loop
	earlyCount := 0
	for _, err := range document.SplitIter(loader.Load(ctx), splitter) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		earlyCount++
		if earlyCount == 1 {
			break
		}
	}
	if earlyCount != 1 {
		t.Errorf("expected early break after 1 chunk, got %d", earlyCount)
	}

	// Test upstream error propagation
	upstreamErr := errors.New("upstream failed")
	failingLoader := document.LoaderFunc(func(ctx context.Context) iter.Seq2[document.Document, error] {
		return func(yield func(document.Document, error) bool) {
			yield(document.Document{}, upstreamErr)
		}
	})

	failingStream := document.SplitIter(failingLoader.Load(ctx), splitter)
	var receivedErr error
	for _, err := range failingStream {
		if err != nil {
			receivedErr = err
			break
		}
	}
	if !errors.Is(receivedErr, upstreamErr) {
		t.Fatalf("expected upstreamErr, got %v", receivedErr)
	}
}

func TestLoadAll_NilLoader(t *testing.T) {
	_, err := document.LoadAll(context.Background(), nil)
	if err == nil {
		t.Fatalf("expected error for nil loader, got nil")
	}
}

func TestLoaderFunc(t *testing.T) {
	ctx := context.Background()
	lf := document.LoaderFunc(func(ctx context.Context) iter.Seq2[document.Document, error] {
		return func(yield func(document.Document, error) bool) {
			yield(document.New("from func"), nil)
		}
	})

	docs, err := document.LoadAll(ctx, lf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) != 1 || docs[0].Content != "from func" {
		t.Fatalf("unexpected docs from LoaderFunc: %v", docs)
	}
}

func TestSplitter_EdgeCasesAndOptions(t *testing.T) {
	// Custom separators
	sCustom := document.NewRecursiveCharacterSplitter(
		document.WithSeparators("|", ","),
		document.WithChunkSize(5),
		document.WithChunkOverlap(1),
	)
	chunks := sCustom.SplitText("a|b|c,d|e")
	if len(chunks) == 0 {
		t.Fatalf("expected chunks with custom separators")
	}

	// KeepSeparator true
	sKeep := document.NewRecursiveCharacterSplitter(
		document.WithSeparators("\n\n", "\n"),
		document.WithChunkSize(15),
		document.WithChunkOverlap(0),
		document.WithKeepSeparator(true),
	)
	keepChunks := sKeep.SplitText("hello\nworld\nfoo\nbar")
	if len(keepChunks) == 0 {
		t.Fatalf("expected chunks with KeepSeparator")
	}

	// TrimWhitespace false
	sNoTrim := document.NewRecursiveCharacterSplitter(
		document.WithChunkSize(20),
		document.WithTrimWhitespace(false),
	)
	noTrimChunks := sNoTrim.SplitText("  padded text  ")
	if len(noTrimChunks) > 0 && !strings.HasPrefix(noTrimChunks[0], "  ") {
		t.Errorf("expected whitespace preserved with TrimWhitespace=false, got %q", noTrimChunks[0])
	}

	// Empty text
	if sCustom.SplitText("") != nil {
		t.Errorf("expected nil for empty text")
	}

	// Empty chunks for EnrichChunkMetadata
	if document.EnrichChunkMetadata(document.New("test"), nil) != nil {
		t.Errorf("expected nil for empty chunks in EnrichChunkMetadata")
	}

	// MarkdownSplitter empty text
	ms := document.NewMarkdownSplitter()
	if ms.SplitText("") != nil {
		t.Errorf("expected nil for empty markdown text")
	}
	if len(ms.SplitDocuments(nil)) != 0 {
		t.Errorf("expected empty for nil docs")
	}
}

func TestJSONLoader_Malformed(t *testing.T) {
	ctx := context.Background()

	// Malformed JSON stream
	lBad := document.NewJSONLoader(strings.NewReader(`{invalid json`))
	_, err := document.LoadAll(ctx, lBad)
	if err == nil {
		t.Fatalf("expected error for malformed JSON, got nil")
	}

	// Malformed JSONL
	lBadJSONL := document.NewJSONLoader(strings.NewReader(`{"valid": 1}`+"\n{invalid json}\n"), document.WithJSONLines(true))
	_, err = document.LoadAll(ctx, lBadJSONL)
	if err == nil {
		t.Fatalf("expected error for malformed JSONL, got nil")
	}
}
