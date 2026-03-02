package parser

import (
	"log/slog"
	"os"
	"strings"

	"golang.org/x/net/html"
)

// ParseHTML extracts plain text from an HTML file.
func ParseHTML(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		slog.Error("failed to read HTML file", "path", path, "err", err)
		return ""
	}
	return HTMLToText(string(data))
}

// HTMLToText converts HTML content to plain text, stripping all tags.
func HTMLToText(content string) string {
	if strings.TrimSpace(content) == "" {
		return ""
	}

	doc, err := html.Parse(strings.NewReader(content))
	if err != nil {
		slog.Warn("failed to parse HTML", "err", err)
		return content // Return raw content as fallback.
	}

	var sb strings.Builder
	extractText(doc, &sb)

	// Clean up: deduplicate blank lines.
	lines := strings.Split(sb.String(), "\n")
	var cleaned []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	return strings.Join(cleaned, "\n")
}

func extractText(n *html.Node, sb *strings.Builder) {
	if n.Type == html.TextNode {
		text := strings.TrimSpace(n.Data)
		if text != "" {
			sb.WriteString(text)
			sb.WriteString("\n")
		}
	}

	// Add line breaks for block elements.
	isBlock := n.Type == html.ElementNode && isBlockElement(n.Data)
	if isBlock && sb.Len() > 0 {
		sb.WriteString("\n")
	}

	for c := n.FirstChild; c != nil; c = c.NextSibling {
		// Skip script and style elements.
		if c.Type == html.ElementNode && (c.Data == "script" || c.Data == "style") {
			continue
		}
		extractText(c, sb)
	}
}

func isBlockElement(tag string) bool {
	switch tag {
	case "p", "div", "br", "h1", "h2", "h3", "h4", "h5", "h6",
		"li", "tr", "th", "td", "blockquote", "pre", "section",
		"article", "header", "footer", "nav", "main":
		return true
	}
	return false
}
