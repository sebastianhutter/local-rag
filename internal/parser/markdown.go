package parser

import (
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// MarkdownDocument is the parsed representation of an Obsidian markdown note.
type MarkdownDocument struct {
	Title       string
	BodyText    string
	Frontmatter map[string]any
	Tags        []string
	Links       []string
}

var (
	frontmatterRE = regexp.MustCompile(`(?s)\A---\s*\n(.*?\n)---\s*\n?`)
	wikilinkRE    = regexp.MustCompile(`\[\[([^\]]+)\]\]`)
	embedRE       = regexp.MustCompile(`!\[\[([^\]]+)\]\]`)
	dataviewRE    = regexp.MustCompile("(?s)```dataview\\s*\\n.*?\\n```")
	inlineTagRE   = regexp.MustCompile(`(?:^|\s)#([\w][\w/\-]*)`)
	codeBlockRE   = regexp.MustCompile("(?s)```.*?```")
	inlineCodeRE  = regexp.MustCompile("`[^`]+`")
	headingLineRE = regexp.MustCompile(`(?m)^#{1,6}\s+`)
	excessiveNL   = regexp.MustCompile(`\n{3,}`)
)

// ParseMarkdown parses an Obsidian-flavored markdown note.
func ParseMarkdown(text, filename string) *MarkdownDocument {
	frontmatter, body := extractFrontmatter(text)

	body = dataviewRE.ReplaceAllString(body, "")

	body, _ = extractEmbeds(body)

	var links []string
	body, links = convertWikilinks(body)

	tags := extractTags(body, frontmatter)

	title, _ := frontmatter["title"].(string)
	if title == "" {
		ext := filepath.Ext(filename)
		title = strings.TrimSuffix(filename, ext)
	}

	body = excessiveNL.ReplaceAllString(body, "\n\n")
	body = strings.TrimSpace(body)

	return &MarkdownDocument{
		Title:       title,
		BodyText:    body,
		Frontmatter: frontmatter,
		Tags:        tags,
		Links:       links,
	}
}

// ParseMarkdownFile reads a markdown file and parses it.
func ParseMarkdownFile(path string) *MarkdownDocument {
	data, err := os.ReadFile(path)
	if err != nil {
		slog.Error("failed to read markdown file", "path", path, "err", err)
		return &MarkdownDocument{Title: filepath.Base(path)}
	}
	return ParseMarkdown(string(data), filepath.Base(path))
}

func extractFrontmatter(text string) (map[string]any, string) {
	match := frontmatterRE.FindStringSubmatchIndex(text)
	if match == nil {
		return map[string]any{}, text
	}

	yamlStr := text[match[2]:match[3]]
	remaining := text[match[1]:]

	var fm map[string]any
	if err := yaml.Unmarshal([]byte(yamlStr), &fm); err != nil {
		slog.Warn("failed to parse frontmatter", "err", err)
		return map[string]any{}, text
	}
	if fm == nil {
		fm = map[string]any{}
	}
	return fm, remaining
}

func extractEmbeds(text string) (string, []string) {
	var embeds []string
	cleaned := embedRE.ReplaceAllStringFunc(text, func(match string) string {
		sub := embedRE.FindStringSubmatch(match)
		if len(sub) > 1 {
			embeds = append(embeds, sub[1])
		}
		return ""
	})
	return cleaned, embeds
}

func convertWikilinks(text string) (string, []string) {
	var links []string
	converted := wikilinkRE.ReplaceAllStringFunc(text, func(match string) string {
		sub := wikilinkRE.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		inner := sub[1]
		if idx := strings.Index(inner, "|"); idx >= 0 {
			target := strings.TrimSpace(inner[:idx])
			display := strings.TrimSpace(inner[idx+1:])
			links = append(links, target)
			return display + " (" + target + ")"
		}
		links = append(links, strings.TrimSpace(inner))
		return strings.TrimSpace(inner)
	})
	return converted, links
}

func extractTags(text string, frontmatter map[string]any) []string {
	seen := make(map[string]bool)
	var tags []string

	addTag := func(t string) {
		if !seen[t] {
			seen[t] = true
			tags = append(tags, t)
		}
	}

	// Tags from frontmatter.
	if fmTags, ok := frontmatter["tags"]; ok {
		switch v := fmTags.(type) {
		case []any:
			for _, t := range v {
				if s, ok := t.(string); ok && s != "" {
					addTag(s)
				}
			}
		case string:
			for _, t := range strings.Split(v, ",") {
				t = strings.TrimSpace(t)
				if t != "" {
					addTag(t)
				}
			}
		}
	}

	// Strip code blocks and inline code.
	cleaned := codeBlockRE.ReplaceAllString(text, "")
	cleaned = inlineCodeRE.ReplaceAllString(cleaned, "")

	// Strip heading lines so # aren't matched.
	cleaned = headingLineRE.ReplaceAllString(cleaned, "")

	for _, match := range inlineTagRE.FindAllStringSubmatch(cleaned, -1) {
		if len(match) > 1 {
			addTag(match[1])
		}
	}

	return tags
}
