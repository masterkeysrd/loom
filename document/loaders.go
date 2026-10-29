package document

import (
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"iter"
	"path"
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

// -------------------------------------------------------------------------
// TextLoader
// -------------------------------------------------------------------------

// TextLoader yields a single document containing an in-memory string.
type TextLoader struct {
	text     string
	metadata map[string]any
}

// NewTextLoader creates a TextLoader with the given text and optional metadata.
func NewTextLoader(text string, metadata ...map[string]any) *TextLoader {
	return &TextLoader{
		text:     text,
		metadata: mergeMetadata(metadata...),
	}
}

// Load yields a single document with the loader's text and cloned metadata.
func (l *TextLoader) Load(ctx context.Context) iter.Seq2[Document, error] {
	return func(yield func(Document, error) bool) {
		if err := ctx.Err(); err != nil {
			yield(Document{}, err)
			return
		}
		doc := Document{
			Content:  l.text,
			Metadata: CloneMetadata(l.metadata),
		}
		yield(doc, nil)
	}
}

// -------------------------------------------------------------------------
// ReaderLoader
// -------------------------------------------------------------------------

// ReaderLoader reads all bytes from an io.Reader into a single document.
type ReaderLoader struct {
	r        io.Reader
	source   string
	metadata map[string]any
}

// NewReaderLoader creates a ReaderLoader from r. If source is non-empty,
// it is added to the document's metadata under the "source" key.
func NewReaderLoader(r io.Reader, source string, metadata ...map[string]any) *ReaderLoader {
	return &ReaderLoader{
		r:        r,
		source:   source,
		metadata: mergeMetadata(metadata...),
	}
}

// Load reads all bytes from the underlying reader and yields a single document.
func (l *ReaderLoader) Load(ctx context.Context) iter.Seq2[Document, error] {
	return func(yield func(Document, error) bool) {
		if err := ctx.Err(); err != nil {
			yield(Document{}, err)
			return
		}
		if l.r == nil {
			yield(Document{}, errors.New("document: nil reader"))
			return
		}
		data, err := io.ReadAll(l.r)
		if err != nil {
			yield(Document{}, err)
			return
		}
		meta := CloneMetadata(l.metadata)
		if l.source != "" {
			if meta == nil {
				meta = make(map[string]any)
			}
			meta["source"] = l.source
		}
		doc := Document{
			Content:  string(data),
			Metadata: meta,
		}
		yield(doc, nil)
	}
}

// -------------------------------------------------------------------------
// FSLoader
// -------------------------------------------------------------------------

// FSLoader lazily walks an fs.FS and yields a document for each matching file.
type FSLoader struct {
	fsys     fs.FS
	patterns []string
}

// NewFSLoader creates an FSLoader that walks fsys. If patterns are provided,
// only files whose relative path or base name matches at least one pattern
// (via path.Match) are loaded.
func NewFSLoader(fsys fs.FS, patterns ...string) *FSLoader {
	return &FSLoader{
		fsys:     fsys,
		patterns: patterns,
	}
}

// Load walks the filesystem lazily, yielding each file as a Document.
func (l *FSLoader) Load(ctx context.Context) iter.Seq2[Document, error] {
	return func(yield func(Document, error) bool) {
		if err := ctx.Err(); err != nil {
			yield(Document{}, err)
			return
		}
		if l.fsys == nil {
			yield(Document{}, errors.New("document: nil filesystem"))
			return
		}
		walkErr := fs.WalkDir(l.fsys, ".", func(filePath string, d fs.DirEntry, err error) error {
			if err != nil {
				if !yield(Document{}, err) {
					return fs.SkipAll
				}
				return nil
			}
			if d.IsDir() {
				return nil
			}
			if err := ctx.Err(); err != nil {
				yield(Document{}, err)
				return fs.SkipAll
			}
			if len(l.patterns) > 0 {
				matched := false
				base := path.Base(filePath)
				for _, pat := range l.patterns {
					if m, _ := path.Match(pat, filePath); m {
						matched = true
						break
					}
					if m, _ := path.Match(pat, base); m {
						matched = true
						break
					}
				}
				if !matched {
					return nil
				}
			}

			data, err := fs.ReadFile(l.fsys, filePath)
			if err != nil {
				if !yield(Document{}, err) {
					return fs.SkipAll
				}
				return nil
			}

			doc := Document{
				ID:      filePath,
				Content: string(data),
				Metadata: map[string]any{
					"source": filePath,
					"path":   filePath,
					"name":   path.Base(filePath),
					"ext":    path.Ext(filePath),
				},
			}
			if !yield(doc, nil) {
				return fs.SkipAll
			}
			return nil
		})
		if walkErr != nil && !errors.Is(walkErr, fs.SkipAll) {
			yield(Document{}, walkErr)
		}
	}
}

// -------------------------------------------------------------------------
// CSVLoader
// -------------------------------------------------------------------------

// CSVLoader reads CSV records row-by-row into documents.
type CSVLoader struct {
	r           io.Reader
	source      string
	contentCols []string
	delimiter   rune
}

// CSVOption configures a CSVLoader.
type CSVOption func(*CSVLoader)

// WithCSVSource sets the source metadata for documents produced by CSVLoader.
func WithCSVSource(source string) CSVOption {
	return func(l *CSVLoader) {
		l.source = source
	}
}

// WithCSVContentColumns configures which columns form the Document Content.
// If set, only these columns form Content; remaining columns are placed in Metadata.
// If not set, all columns are formatted as "col: value\n..." in Content and also stored in Metadata.
func WithCSVContentColumns(cols ...string) CSVOption {
	return func(l *CSVLoader) {
		l.contentCols = cols
	}
}

// WithCSVDelimiter sets the delimiter rune for CSV parsing (default: ',').
func WithCSVDelimiter(comma rune) CSVOption {
	return func(l *CSVLoader) {
		l.delimiter = comma
	}
}

// NewCSVLoader creates a CSVLoader reading from r with the specified options.
func NewCSVLoader(r io.Reader, opts ...CSVOption) *CSVLoader {
	l := &CSVLoader{
		r:         r,
		delimiter: ',',
	}
	for _, opt := range opts {
		opt(l)
	}
	return l
}

// Load lazily reads CSV records and yields one Document per row.
func (l *CSVLoader) Load(ctx context.Context) iter.Seq2[Document, error] {
	return func(yield func(Document, error) bool) {
		if err := ctx.Err(); err != nil {
			yield(Document{}, err)
			return
		}
		if l.r == nil {
			yield(Document{}, errors.New("document: nil reader"))
			return
		}
		reader := csv.NewReader(l.r)
		reader.Comma = l.delimiter
		reader.FieldsPerRecord = -1 // Allow variable number of fields per row

		headers, err := reader.Read()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			yield(Document{}, err)
			return
		}

		contentColSet := make(map[string]bool, len(l.contentCols))
		for _, c := range l.contentCols {
			contentColSet[c] = true
		}

		rowIndex := 0
		for {
			if err := ctx.Err(); err != nil {
				yield(Document{}, err)
				return
			}
			record, err := reader.Read()
			if err != nil {
				if errors.Is(err, io.EOF) {
					return
				}
				yield(Document{}, err)
				return
			}
			rowIndex++

			rowMap := make(map[string]string, len(headers))
			for i, h := range headers {
				if i < len(record) {
					rowMap[h] = record[i]
				} else {
					rowMap[h] = ""
				}
			}

			meta := make(map[string]any)
			meta["row"] = rowIndex
			if l.source != "" {
				meta["source"] = l.source
			}

			var content string
			if len(l.contentCols) > 0 {
				if len(l.contentCols) == 1 {
					content = rowMap[l.contentCols[0]]
				} else {
					var parts []string
					for _, col := range l.contentCols {
						parts = append(parts, fmt.Sprintf("%s: %s", col, rowMap[col]))
					}
					content = strings.Join(parts, "\n")
				}
				for _, h := range headers {
					if !contentColSet[h] {
						meta[h] = rowMap[h]
					}
				}
			} else {
				var parts []string
				for _, h := range headers {
					val := rowMap[h]
					parts = append(parts, fmt.Sprintf("%s: %s", h, val))
					meta[h] = val
				}
				content = strings.Join(parts, "\n")
			}

			doc := Document{
				Content:  content,
				Metadata: meta,
			}
			if !yield(doc, nil) {
				return
			}
		}
	}
}

// -------------------------------------------------------------------------
// JSONLoader
// -------------------------------------------------------------------------

// JSONLoader decodes documents from a JSON stream (array, single object, or JSONL).
type JSONLoader struct {
	r          io.Reader
	source     string
	contentKey string
	jsonLines  bool
}

// JSONOption configures a JSONLoader.
type JSONOption func(*JSONLoader)

// WithJSONSource sets the source metadata for documents produced by JSONLoader.
func WithJSONSource(source string) JSONOption {
	return func(l *JSONLoader) {
		l.source = source
	}
}

// WithJSONContentKey sets the field name to use as Document Content.
// If set and a JSON object has a string field matching key, that string is used
// as Content and all other fields are placed in Metadata.
// Otherwise, the entire JSON value is marshaled as Content.
func WithJSONContentKey(key string) JSONOption {
	return func(l *JSONLoader) {
		l.contentKey = key
	}
}

// WithJSONLines enables reading newline-delimited JSON objects line-by-line.
func WithJSONLines(jsonl bool) JSONOption {
	return func(l *JSONLoader) {
		l.jsonLines = jsonl
	}
}

// NewJSONLoader creates a JSONLoader reading from r with the specified options.
func NewJSONLoader(r io.Reader, opts ...JSONOption) *JSONLoader {
	l := &JSONLoader{r: r}
	for _, opt := range opts {
		opt(l)
	}
	return l
}

// Load lazily reads JSON data and yields documents.
func (l *JSONLoader) Load(ctx context.Context) iter.Seq2[Document, error] {
	return func(yield func(Document, error) bool) {
		if err := ctx.Err(); err != nil {
			yield(Document{}, err)
			return
		}
		if l.r == nil {
			yield(Document{}, errors.New("document: nil reader"))
			return
		}

		if l.jsonLines {
			l.loadJSONLines(ctx, yield)
			return
		}

		l.loadJSONStream(ctx, yield)
	}
}

func (l *JSONLoader) loadJSONLines(ctx context.Context, yield func(Document, error) bool) {
	reader := bufio.NewReader(l.r)
	for {
		if err := ctx.Err(); err != nil {
			yield(Document{}, err)
			return
		}
		line, err := reader.ReadBytes('\n')
		line = bytes.TrimSpace(line)
		if len(line) > 0 {
			var val any
			if uerr := json.Unmarshal(line, &val); uerr != nil {
				yield(Document{}, uerr)
				return
			}
			doc := l.docFromValue(val)
			if !yield(doc, nil) {
				return
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			yield(Document{}, err)
			return
		}
	}
}

func (l *JSONLoader) loadJSONStream(ctx context.Context, yield func(Document, error) bool) {
	br := bufio.NewReader(l.r)
	b, err := peekFirstNonWhitespace(br)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return
		}
		yield(Document{}, err)
		return
	}

	dec := json.NewDecoder(br)
	if b == '[' {
		// Array of objects / values
		_, err := dec.Token() // consume '['
		if err != nil {
			yield(Document{}, err)
			return
		}
		for dec.More() {
			if err := ctx.Err(); err != nil {
				yield(Document{}, err)
				return
			}
			var val any
			if err := dec.Decode(&val); err != nil {
				yield(Document{}, err)
				return
			}
			doc := l.docFromValue(val)
			if !yield(doc, nil) {
				return
			}
		}
		_, _ = dec.Token() // consume ']'
	} else {
		// Single JSON object or value
		if err := ctx.Err(); err != nil {
			yield(Document{}, err)
			return
		}
		var val any
		if err := dec.Decode(&val); err != nil {
			yield(Document{}, err)
			return
		}
		doc := l.docFromValue(val)
		yield(doc, nil)
	}
}

func (l *JSONLoader) docFromValue(val any) Document {
	meta := make(map[string]any)
	if l.source != "" {
		meta["source"] = l.source
	}

	if sVal, isStr := val.(string); isStr {
		if len(meta) == 0 {
			meta = nil
		}
		return Document{
			Content:  sVal,
			Metadata: meta,
		}
	}

	if obj, ok := val.(map[string]any); ok {
		if l.contentKey != "" {
			if cVal, exists := obj[l.contentKey]; exists {
				if sVal, isStr := cVal.(string); isStr {
					for k, v := range obj {
						if k != l.contentKey {
							meta[k] = v
						}
					}
					if len(meta) == 0 {
						meta = nil
					}
					return Document{
						Content:  sVal,
						Metadata: meta,
					}
				}
			}
		}
	}

	b, err := json.Marshal(val)
	var content string
	if err == nil {
		content = string(b)
	}
	if len(meta) == 0 {
		meta = nil
	}
	return Document{
		Content:  content,
		Metadata: meta,
	}
}

func peekFirstNonWhitespace(br *bufio.Reader) (byte, error) {
	for {
		b, err := br.ReadByte()
		if err != nil {
			return 0, err
		}
		if !unicode.IsSpace(rune(b)) {
			_ = br.UnreadByte()
			return b, nil
		}
	}
}

// -------------------------------------------------------------------------
// HTMLLoader
// -------------------------------------------------------------------------

// HTMLLoader parses HTML from an io.Reader and extracts visible text content and metadata.
type HTMLLoader struct {
	r        io.Reader
	source   string
	metadata map[string]any
}

// HTMLOption configures an HTMLLoader.
type HTMLOption func(*HTMLLoader)

// WithHTMLSource sets the source metadata for the document produced by HTMLLoader.
func WithHTMLSource(source string) HTMLOption {
	return func(l *HTMLLoader) {
		l.source = source
	}
}

// WithHTMLMetadata merges additional metadata into the document produced by HTMLLoader.
func WithHTMLMetadata(meta map[string]any) HTMLOption {
	return func(l *HTMLLoader) {
		l.metadata = mergeMetadata(l.metadata, meta)
	}
}

// NewHTMLLoader creates an HTMLLoader reading HTML content from r with the specified options.
func NewHTMLLoader(r io.Reader, opts ...HTMLOption) *HTMLLoader {
	l := &HTMLLoader{
		r: r,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(l)
		}
	}
	return l
}

var htmlIgnoredTags = map[string]bool{
	"script":   true,
	"style":    true,
	"noscript": true,
	"svg":      true,
	"template": true,
	"head":     true,
}

var htmlBlockTags = map[string]bool{
	"p":          true,
	"div":        true,
	"section":    true,
	"article":    true,
	"header":     true,
	"footer":     true,
	"main":       true,
	"aside":      true,
	"nav":        true,
	"h1":         true,
	"h2":         true,
	"h3":         true,
	"h4":         true,
	"h5":         true,
	"h6":         true,
	"li":         true,
	"tr":         true,
	"blockquote": true,
	"pre":        true,
	"br":         true,
	"hr":         true,
}

// Load parses HTML from the underlying reader and yields a single Document.
func (l *HTMLLoader) Load(ctx context.Context) iter.Seq2[Document, error] {
	return func(yield func(Document, error) bool) {
		if err := ctx.Err(); err != nil {
			yield(Document{}, err)
			return
		}
		if l.r == nil {
			yield(Document{}, errors.New("document: nil reader"))
			return
		}

		docNode, err := html.Parse(l.r)
		if err != nil {
			yield(Document{}, err)
			return
		}

		meta := CloneMetadata(l.metadata)
		if l.source != "" {
			if meta == nil {
				meta = make(map[string]any)
			}
			if _, exists := meta["source"]; !exists {
				meta["source"] = l.source
			}
		}

		extractHTMLMetadata(docNode, &meta)

		var sb strings.Builder
		extractHTMLVisibleText(docNode, &sb)

		content := cleanHTMLWhitespace(sb.String())

		if len(meta) == 0 {
			meta = nil
		}

		if err := ctx.Err(); err != nil {
			yield(Document{}, err)
			return
		}

		doc := Document{
			Content:  content,
			Metadata: meta,
		}
		yield(doc, nil)
	}
}

func extractHTMLMetadata(n *html.Node, meta *map[string]any) {
	if n == nil {
		return
	}
	if n.Type == html.ElementNode {
		tag := strings.ToLower(n.Data)
		switch tag {
		case "title":
			if *meta == nil || (*meta)["title"] == nil || (*meta)["title"] == "" {
				titleText := strings.TrimSpace(extractNodeRawText(n))
				if titleText != "" {
					if *meta == nil {
						*meta = make(map[string]any)
					}
					(*meta)["title"] = titleText
				}
			}
		case "meta":
			if *meta == nil || (*meta)["description"] == nil || (*meta)["description"] == "" {
				var isDesc bool
				var contentVal string
				for _, attr := range n.Attr {
					if strings.EqualFold(attr.Key, "name") && strings.EqualFold(attr.Val, "description") {
						isDesc = true
					}
					if strings.EqualFold(attr.Key, "content") {
						contentVal = attr.Val
					}
				}
				if isDesc {
					contentVal = strings.TrimSpace(contentVal)
					if contentVal != "" {
						if *meta == nil {
							*meta = make(map[string]any)
						}
						(*meta)["description"] = contentVal
					}
				}
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		extractHTMLMetadata(c, meta)
	}
}

func extractNodeRawText(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(curr *html.Node) {
		if curr == nil {
			return
		}
		if curr.Type == html.TextNode {
			sb.WriteString(curr.Data)
		}
		for c := curr.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return sb.String()
}

func extractHTMLVisibleText(n *html.Node, sb *strings.Builder) {
	if n == nil {
		return
	}
	if n.Type == html.ElementNode {
		tag := strings.ToLower(n.Data)
		if htmlIgnoredTags[tag] {
			return
		}
		if tag == "br" || tag == "hr" {
			sb.WriteString("\n")
			return
		}
		isBlock := htmlBlockTags[tag]
		if isBlock {
			sb.WriteString("\n")
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			extractHTMLVisibleText(c, sb)
		}
		if isBlock {
			sb.WriteString("\n")
		}
		return
	}
	if n.Type == html.TextNode {
		sb.WriteString(n.Data)
		return
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		extractHTMLVisibleText(c, sb)
	}
}

func cleanHTMLWhitespace(s string) string {
	lines := strings.Split(s, "\n")
	var cleaned []string
	consecutiveEmpty := 0

	for _, line := range lines {
		trimmed := strings.Join(strings.Fields(line), " ")
		if trimmed == "" {
			if consecutiveEmpty < 1 && len(cleaned) > 0 {
				cleaned = append(cleaned, "")
				consecutiveEmpty++
			}
		} else {
			cleaned = append(cleaned, trimmed)
			consecutiveEmpty = 0
		}
	}

	for len(cleaned) > 0 && cleaned[len(cleaned)-1] == "" {
		cleaned = cleaned[:len(cleaned)-1]
	}

	return strings.Join(cleaned, "\n")
}
