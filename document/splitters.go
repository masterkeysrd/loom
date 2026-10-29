package document

import (
	"fmt"
	"maps"
	"strings"
)

// -------------------------------------------------------------------------
// RecursiveCharacterSplitter
// -------------------------------------------------------------------------

// RecursiveCharacterSplitter splits text recursively using a prioritized list of separators.
type RecursiveCharacterSplitter struct {
	cfg SplitterConfig
}

// NewRecursiveCharacterSplitter creates a RecursiveCharacterSplitter with the given options.
// Default separators are []string{"\n\n", "\n", " ", ""}.
func NewRecursiveCharacterSplitter(opts ...SplitterOption) *RecursiveCharacterSplitter {
	return &RecursiveCharacterSplitter{cfg: newSplitterConfig(opts...)}
}

// SplitText splits text into chunks according to ChunkSize, ChunkOverlap, and Separators.
func (s *RecursiveCharacterSplitter) SplitText(text string) []string {
	if text == "" {
		return nil
	}
	return s.splitTextWithSeparators(text, s.cfg.Separators)
}

// SplitDocuments splits each document in docs into chunks, enriching chunk metadata.
func (s *RecursiveCharacterSplitter) SplitDocuments(docs []Document) []Document {
	var out []Document
	for _, doc := range docs {
		chunks := s.SplitText(doc.Content)
		enriched := EnrichChunkMetadata(doc, chunks)
		out = append(out, enriched...)
	}
	return out
}

func (s *RecursiveCharacterSplitter) splitTextWithSeparators(text string, separators []string) []string {
	if text == "" {
		return nil
	}

	var separator string
	var remainingSeps []string
	found := false

	for i, sep := range separators {
		if sep == "" {
			separator = ""
			remainingSeps = nil
			found = true
			break
		}
		if strings.Contains(text, sep) {
			separator = sep
			remainingSeps = separators[i+1:]
			found = true
			break
		}
	}

	if !found {
		separator = ""
		remainingSeps = nil
	}

	var splits []string
	if separator == "" {
		for _, r := range text {
			splits = append(splits, string(r))
		}
	} else if s.cfg.KeepSeparator {
		parts := strings.Split(text, separator)
		if len(parts) > 0 {
			splits = append(splits, parts[0])
			for _, p := range parts[1:] {
				splits = append(splits, separator+p)
			}
		}
	} else {
		splits = strings.Split(text, separator)
	}

	var goodSplits []string
	for _, piece := range splits {
		if s.cfg.TrimWhitespace && strings.TrimSpace(piece) == "" {
			continue
		}
		if s.cfg.LengthFunc(piece) <= s.cfg.ChunkSize || len(remainingSeps) == 0 {
			goodSplits = append(goodSplits, piece)
		} else {
			subSplits := s.splitTextWithSeparators(piece, remainingSeps)
			goodSplits = append(goodSplits, subSplits...)
		}
	}

	return s.mergeSplits(goodSplits, separator)
}

func (s *RecursiveCharacterSplitter) mergeSplits(splits []string, separator string) []string {
	joinSep := separator
	if s.cfg.KeepSeparator || separator == "" {
		joinSep = ""
	}

	var docs []string
	var currentDoc []string
	total := 0
	sepLen := s.cfg.LengthFunc(joinSep)

	for _, piece := range splits {
		pieceLen := s.cfg.LengthFunc(piece)
		currentSepLen := 0
		if len(currentDoc) > 0 {
			currentSepLen = sepLen
		}

		if total+pieceLen+currentSepLen > s.cfg.ChunkSize {
			if len(currentDoc) > 0 {
				doc := strings.Join(currentDoc, joinSep)
				if s.cfg.TrimWhitespace {
					doc = strings.TrimSpace(doc)
				}
				if doc != "" {
					docs = append(docs, doc)
				}

				for len(currentDoc) > 0 && (total > s.cfg.ChunkOverlap || (total+pieceLen+currentSepLen > s.cfg.ChunkSize && total > 0)) {
					popLen := s.cfg.LengthFunc(currentDoc[0])
					currentDoc = currentDoc[1:]
					if len(currentDoc) > 0 {
						total -= (popLen + sepLen)
						currentSepLen = sepLen
					} else {
						total = 0
						currentSepLen = 0
					}
				}
			}
		}

		currentDoc = append(currentDoc, piece)
		if len(currentDoc) == 1 {
			total += pieceLen
		} else {
			total += pieceLen + sepLen
		}
	}

	if len(currentDoc) > 0 {
		doc := strings.Join(currentDoc, joinSep)
		if s.cfg.TrimWhitespace {
			doc = strings.TrimSpace(doc)
		}
		if doc != "" {
			docs = append(docs, doc)
		}
	}

	return docs
}

// -------------------------------------------------------------------------
// MarkdownSplitter
// -------------------------------------------------------------------------

// MarkdownSplitter splits markdown documents while tracking heading hierarchies
// and protecting fenced code blocks from accidental splitting.
type MarkdownSplitter struct {
	cfg SplitterConfig
	rec *RecursiveCharacterSplitter
}

// NewMarkdownSplitter creates a MarkdownSplitter with the given options.
func NewMarkdownSplitter(opts ...SplitterOption) *MarkdownSplitter {
	cfg := newSplitterConfig(opts...)
	return &MarkdownSplitter{
		cfg: cfg,
		rec: &RecursiveCharacterSplitter{cfg: cfg},
	}
}

type markdownSection struct {
	headings map[string]string
	content  string
}

// SplitText splits markdown text into chunks.
func (s *MarkdownSplitter) SplitText(text string) []string {
	sections := s.splitIntoSections(text)
	var out []string
	for _, sec := range sections {
		chunks := s.splitSectionContent(sec.content)
		out = append(out, chunks...)
	}
	return out
}

// SplitDocuments splits each document in docs into chunks, attaching active
// section heading metadata ("h1", "h2", etc.) alongside standard chunk metadata.
func (s *MarkdownSplitter) SplitDocuments(docs []Document) []Document {
	var out []Document
	for _, doc := range docs {
		sections := s.splitIntoSections(doc.Content)
		var docChunks []Document
		for _, sec := range sections {
			chunks := s.splitSectionContent(sec.content)
			for _, chunk := range chunks {
				meta := CloneMetadata(doc.Metadata)
				if meta == nil {
					meta = make(map[string]any)
				}
				for k, v := range sec.headings {
					meta[k] = v
				}
				docChunks = append(docChunks, Document{
					Content:  chunk,
					Metadata: meta,
					Score:    doc.Score,
				})
			}
		}
		total := len(docChunks)
		for i := range docChunks {
			docChunks[i].Metadata["chunk_index"] = i
			docChunks[i].Metadata["chunk_total"] = total
			if doc.ID != "" {
				docChunks[i].Metadata["parent_id"] = doc.ID
				docChunks[i].ID = fmt.Sprintf("%s#%d", doc.ID, i)
			}
		}
		out = append(out, docChunks...)
	}
	return out
}

func (s *MarkdownSplitter) splitIntoSections(text string) []markdownSection {
	if text == "" {
		return nil
	}

	lines := strings.Split(text, "\n")
	var sections []markdownSection
	var currentLines []string
	activeHeadings := make(map[string]string)

	inFence := false
	fenceChar := rune(0)
	fenceLen := 0

	flushSection := func() {
		if len(currentLines) == 0 {
			return
		}
		content := strings.Join(currentLines, "\n")
		if s.cfg.TrimWhitespace {
			content = strings.TrimSpace(content)
		}
		if content != "" {
			hCopy := make(map[string]string, len(activeHeadings))
			maps.Copy(hCopy, activeHeadings)
			sections = append(sections, markdownSection{
				headings: hCopy,
				content:  content,
			})
		}
		currentLines = nil
	}

	for _, rawLine := range lines {
		line := strings.TrimSuffix(rawLine, "\r")

		if !inFence {
			if ok, ch, cnt := parseFenceStart(line); ok {
				inFence = true
				fenceChar = ch
				fenceLen = cnt
				currentLines = append(currentLines, line)
				continue
			}

			if level, title, ok := parseHeading(line); ok {
				flushSection()
				key := fmt.Sprintf("h%d", level)
				activeHeadings[key] = title
				for i := level + 1; i <= 6; i++ {
					delete(activeHeadings, fmt.Sprintf("h%d", i))
				}
				currentLines = append(currentLines, line)
				continue
			}

			currentLines = append(currentLines, line)
		} else {
			currentLines = append(currentLines, line)
			if parseFenceEnd(line, fenceChar, fenceLen) {
				inFence = false
				fenceChar = 0
				fenceLen = 0
			}
		}
	}
	flushSection()

	return sections
}

func (s *MarkdownSplitter) splitSectionContent(content string) []string {
	if s.cfg.TrimWhitespace {
		content = strings.TrimSpace(content)
	}
	if content == "" {
		return nil
	}
	if s.cfg.LengthFunc(content) <= s.cfg.ChunkSize {
		return []string{content}
	}

	blocks := splitMarkdownBlocks(content)

	var pieces []string
	for _, block := range blocks {
		if s.cfg.LengthFunc(block) <= s.cfg.ChunkSize {
			pieces = append(pieces, block)
		} else {
			subPieces := s.rec.SplitText(block)
			pieces = append(pieces, subPieces...)
		}
	}

	return s.rec.mergeSplits(pieces, "\n\n")
}

func splitMarkdownBlocks(content string) []string {
	lines := strings.Split(content, "\n")
	var blocks []string
	var current []string

	inFence := false
	fenceChar := rune(0)
	fenceLen := 0

	flushCurrent := func() {
		if len(current) == 0 {
			return
		}
		blk := strings.Join(current, "\n")
		blk = strings.TrimSpace(blk)
		if blk != "" {
			blocks = append(blocks, blk)
		}
		current = nil
	}

	for _, rawLine := range lines {
		line := strings.TrimSuffix(rawLine, "\r")
		if !inFence {
			if ok, ch, cnt := parseFenceStart(line); ok {
				flushCurrent()
				inFence = true
				fenceChar = ch
				fenceLen = cnt
				current = append(current, line)
				continue
			}
			if strings.TrimSpace(line) == "" {
				flushCurrent()
				continue
			}
			current = append(current, line)
		} else {
			current = append(current, line)
			if parseFenceEnd(line, fenceChar, fenceLen) {
				inFence = false
				fenceChar = 0
				fenceLen = 0
				flushCurrent()
			}
		}
	}
	flushCurrent()
	return blocks
}

func parseFenceStart(line string) (bool, rune, int) {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 {
		return false, 0, 0
	}
	if len(trimmed) < 3 {
		return false, 0, 0
	}
	r := rune(trimmed[0])
	if r != '`' && r != '~' {
		return false, 0, 0
	}
	cnt := 0
	for _, ch := range trimmed {
		if ch == r {
			cnt++
		} else {
			break
		}
	}
	if cnt < 3 {
		return false, 0, 0
	}
	if r == '`' && strings.ContainsRune(trimmed[cnt:], '`') {
		return false, 0, 0
	}
	return true, r, cnt
}

func parseFenceEnd(line string, openChar rune, openCount int) bool {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 {
		return false
	}
	if len(trimmed) < openCount {
		return false
	}
	cnt := 0
	for _, ch := range trimmed {
		if ch == openChar {
			cnt++
		} else {
			break
		}
	}
	if cnt < openCount {
		return false
	}
	rest := strings.TrimSpace(trimmed[cnt:])
	return rest == ""
}

func parseHeading(line string) (int, string, bool) {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 {
		return 0, "", false
	}
	if !strings.HasPrefix(trimmed, "#") {
		return 0, "", false
	}
	cnt := 0
	for _, ch := range trimmed {
		if ch == '#' {
			cnt++
		} else {
			break
		}
	}
	if cnt < 1 || cnt > 6 {
		return 0, "", false
	}
	rest := trimmed[cnt:]
	if len(rest) == 0 {
		return cnt, "", true
	}
	if rest[0] != ' ' && rest[0] != '\t' {
		return 0, "", false
	}
	title := strings.TrimSpace(rest)
	// Per CommonMark, a closing sequence of '#' characters is only stripped
	// if it is preceded by spaces or tabs (or makes up the entire title).
	trimmedHashes := strings.TrimRight(title, "#")
	if trimmedHashes == "" || strings.HasSuffix(trimmedHashes, " ") || strings.HasSuffix(trimmedHashes, "\t") {
		title = strings.TrimSpace(trimmedHashes)
	}
	return cnt, title, true
}
