// Package chunker provides text chunking strategies for different content types.
package chunker

import (
	"regexp"
	"sort"
	"strings"
)

// Chunk represents a piece of text with metadata ready for embedding.
type Chunk struct {
	Text       string
	Title      string
	Metadata   map[string]any
	ChunkIndex int
}

// WordCount estimates the token count by splitting on whitespace.
func WordCount(text string) int {
	return len(strings.Fields(text))
}

// SplitIntoWindows splits text into overlapping word-based windows.
func SplitIntoWindows(text string, chunkSize, overlap int) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	if len(words) <= chunkSize {
		return []string{text}
	}

	var chunks []string
	start := 0
	for start < len(words) {
		end := start + chunkSize
		if end > len(words) {
			end = len(words)
		}
		chunks = append(chunks, strings.Join(words[start:end], " "))
		if end >= len(words) {
			break
		}
		start = end - overlap
	}
	return chunks
}

var headingPattern = regexp.MustCompile(`(?m)^(#{1,6})\s+(.+)$`)

// ChunkMarkdown splits markdown text on headings, preserving heading path as context prefix.
func ChunkMarkdown(text, title string, chunkSize, overlap int) []Chunk {
	if strings.TrimSpace(text) == "" {
		return []Chunk{{Text: "", Title: title, Metadata: map[string]any{}, ChunkIndex: 0}}
	}

	type section struct {
		headingPath string
		content     string
	}

	// Split on headings.
	matches := headingPattern.FindAllStringSubmatchIndex(text, -1)

	var sections []section

	if len(matches) == 0 {
		// No headings — treat entire text as one section.
		sections = append(sections, section{headingPath: "", content: strings.TrimSpace(text)})
	} else {
		// Preamble before first heading.
		preamble := strings.TrimSpace(text[:matches[0][0]])
		if preamble != "" {
			sections = append(sections, section{headingPath: "", content: preamble})
		}

		currentHeadings := make(map[int]string)

		for i, match := range matches {
			level := match[3] - match[2] // length of # chars
			headingText := strings.TrimSpace(text[match[4]:match[5]])

			// Content is everything between this heading and the next (or end).
			contentStart := match[1]
			var contentEnd int
			if i+1 < len(matches) {
				contentEnd = matches[i+1][0]
			} else {
				contentEnd = len(text)
			}
			content := strings.TrimSpace(text[contentStart:contentEnd])

			// Update heading hierarchy.
			currentHeadings[level] = headingText
			// Clear deeper headings.
			for k := range currentHeadings {
				if k > level {
					delete(currentHeadings, k)
				}
			}

			// Build heading path.
			var levels []int
			for k := range currentHeadings {
				levels = append(levels, k)
			}
			sort.Ints(levels)
			var parts []string
			for _, l := range levels {
				parts = append(parts, currentHeadings[l])
			}
			headingPath := strings.Join(parts, " > ")

			if content != "" {
				sections = append(sections, section{headingPath: headingPath, content: content})
			}
		}
	}

	// Chunk each section.
	var chunks []Chunk
	chunkIdx := 0

	for _, sec := range sections {
		prefix := ""
		if sec.headingPath != "" {
			prefix = "[" + sec.headingPath + "] "
		}
		prefixed := prefix + sec.content

		if WordCount(prefixed) <= chunkSize {
			meta := map[string]any{}
			if sec.headingPath != "" {
				meta["heading_path"] = sec.headingPath
			}
			chunks = append(chunks, Chunk{
				Text:       prefixed,
				Title:      title,
				Metadata:   meta,
				ChunkIndex: chunkIdx,
			})
			chunkIdx++
		} else {
			prefixWords := WordCount(prefix)
			windows := SplitIntoWindows(sec.content, chunkSize-prefixWords, overlap)
			for _, w := range windows {
				meta := map[string]any{}
				if sec.headingPath != "" {
					meta["heading_path"] = sec.headingPath
				}
				chunks = append(chunks, Chunk{
					Text:       prefix + w,
					Title:      title,
					Metadata:   meta,
					ChunkIndex: chunkIdx,
				})
				chunkIdx++
			}
		}
	}

	if len(chunks) == 0 {
		chunks = append(chunks, Chunk{
			Text:       strings.TrimSpace(text),
			Title:      title,
			Metadata:   map[string]any{},
			ChunkIndex: 0,
		})
	}

	return chunks
}

var paragraphSplitter = regexp.MustCompile(`\n\s*\n`)

// ChunkEmail chunks an email body, using subject as title context.
func ChunkEmail(subject, body string, chunkSize, overlap int) []Chunk {
	title := subject
	if title == "" {
		title = "(no subject)"
	}

	if strings.TrimSpace(body) == "" {
		text := ""
		if subject != "" {
			text = "Subject: " + subject
		}
		return []Chunk{{Text: text, Title: title, Metadata: map[string]any{}, ChunkIndex: 0}}
	}

	fullText := strings.TrimSpace(body)

	if WordCount(fullText) <= chunkSize {
		return []Chunk{{Text: fullText, Title: title, Metadata: map[string]any{}, ChunkIndex: 0}}
	}

	// Split by double newlines (paragraphs).
	rawParagraphs := paragraphSplitter.Split(fullText, -1)
	var paragraphs []string
	for _, p := range rawParagraphs {
		p = strings.TrimSpace(p)
		if p != "" {
			paragraphs = append(paragraphs, p)
		}
	}

	var chunks []Chunk
	chunkIdx := 0
	currentText := ""

	for _, para := range paragraphs {
		candidate := currentText + " " + para
		if WordCount(candidate) <= chunkSize {
			if currentText == "" {
				currentText = para
			} else {
				currentText = currentText + "\n\n" + para
			}
		} else {
			if currentText != "" {
				chunks = append(chunks, Chunk{
					Text:       currentText,
					Title:      title,
					Metadata:   map[string]any{},
					ChunkIndex: chunkIdx,
				})
				chunkIdx++
			}

			if WordCount(para) > chunkSize {
				windows := SplitIntoWindows(para, chunkSize, overlap)
				for _, w := range windows {
					chunks = append(chunks, Chunk{
						Text:       w,
						Title:      title,
						Metadata:   map[string]any{},
						ChunkIndex: chunkIdx,
					})
					chunkIdx++
				}
				currentText = ""
			} else {
				currentText = para
			}
		}
	}

	if currentText != "" {
		chunks = append(chunks, Chunk{
			Text:       currentText,
			Title:      title,
			Metadata:   map[string]any{},
			ChunkIndex: chunkIdx,
		})
	}

	return chunks
}

// ChunkPlain chunks plain text using fixed-size word windows with overlap.
func ChunkPlain(text, title string, chunkSize, overlap int) []Chunk {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return []Chunk{{Text: "", Title: title, Metadata: map[string]any{}, ChunkIndex: 0}}
	}

	windows := SplitIntoWindows(trimmed, chunkSize, overlap)
	chunks := make([]Chunk, len(windows))
	for i, w := range windows {
		chunks[i] = Chunk{
			Text:       w,
			Title:      title,
			Metadata:   map[string]any{},
			ChunkIndex: i,
		}
	}
	return chunks
}
