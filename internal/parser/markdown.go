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

// Wikilink and embed syntax. Kept as strings so they can be appended to
// codeRegionPattern as a trailing alternative (see replaceOutsideCode).
const (
	embedPattern    = `!\[\[([^\]]+)\]\]`
	wikilinkPattern = `\[\[([^\]]+)\]\]`
)

// codeRegionPattern matches the parts of a markdown note that link extraction
// must leave alone: ``` and ~~~ fenced blocks and inline backtick spans. Both
// fence forms require a closing fence, so an unclosed fence does not swallow
// the rest of the note; the content in between is matched line by line, which
// also allows an empty block. Any amount of leading whitespace is accepted:
// CommonMark caps fence indentation at three spaces, but Obsidian renders a
// deeper one as code all the same, and notes that nest fences several list
// levels down are common.
const codeRegionPattern = "(?m:^[ \t]*`{3,}[^\n]*\n(?:[^\n]*\n)*?[ \t]*`{3,}[ \t]*$)" +
	"|(?m:^[ \t]*~{3,}[^\n]*\n(?:[^\n]*\n)*?[ \t]*~{3,}[ \t]*$)" +
	"|`[^`\n]+`"

var (
	frontmatterRE = regexp.MustCompile(`(?s)\A---\s*\n(.*?\n)---\s*\n?`)
	wikilinkRE    = regexp.MustCompile(wikilinkPattern)
	embedRE       = regexp.MustCompile(embedPattern)

	// Code regions are prepended as earlier alternatives so that a match
	// starting inside code is claimed by the code region and handed back
	// untouched. Without this, bash and Python constructs such as
	// `[[ -z "$x" ]]`, `[[:space:]]` and `Callable[[dict[str, Any]], None]`
	// are read as wikilinks: they produce junk link metadata and, because
	// conversion rewrites the body that gets chunked and embedded, corrupt
	// the indexed code itself.
	embedOutsideCodeRE    = regexp.MustCompile(codeRegionPattern + "|" + embedPattern)
	wikilinkOutsideCodeRE = regexp.MustCompile(codeRegionPattern + "|" + wikilinkPattern)

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

// replaceOutsideCode rewrites every match of target that lies outside a code
// region and leaves code regions byte-identical. combined must be
// codeRegionPattern followed by target's own pattern as the final alternative:
// the regexp engine prefers the leftmost match and, among those, the earliest
// alternative, so anything inside code is returned as-is.
func replaceOutsideCode(text string, combined, target *regexp.Regexp, repl func(match string) string) string {
	return combined.ReplaceAllStringFunc(text, func(match string) string {
		// A code region never matches target in full: it starts with a
		// backtick or a tilde, and an inline span keeps its delimiters.
		if target.FindString(match) != match {
			return match
		}
		return repl(match)
	})
}

func extractEmbeds(text string) (string, []string) {
	var embeds []string
	cleaned := replaceOutsideCode(text, embedOutsideCodeRE, embedRE, func(match string) string {
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
	converted := replaceOutsideCode(text, wikilinkOutsideCodeRE, wikilinkRE, func(match string) string {
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
