package parser

import (
	"reflect"
	"strings"
	"testing"
)

// TestParseMarkdownLinksAndCode covers link extraction against real-world
// notes where bash and Python constructs look like Obsidian wikilinks. Such a
// construct must neither yield a link nor be rewritten in the body, because
// the body is what gets chunked and embedded.
func TestParseMarkdownLinksAndCode(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantLinks  []string
		wantInBody []string
		notInBody  []string
	}{
		{
			name: "bash -z test in fenced block",
			input: "Check the latest tag:\n\n" +
				"```bash\n" +
				"if [[ -z \"$latest\" ]]; then\n" +
				"  echo \"no tag\"\n" +
				"fi\n" +
				"```\n",
			wantInBody: []string{"if [[ -z \"$latest\" ]]; then"},
		},
		{
			name: "bash string comparison in fenced block",
			input: "```sh\n" +
				"read -r confirm\n" +
				"if [[ \"$confirm\" == \"y\" ]]; then run; fi\n" +
				"```\n",
			wantInBody: []string{"if [[ \"$confirm\" == \"y\" ]]; then run; fi"},
		},
		{
			name: "posix character class in fenced block",
			input: "```bash\n" +
				"sed 's/[[:space:]]\\+/ /g' input.txt\n" +
				"```\n",
			wantInBody: []string{"sed 's/[[:space:]]\\+/ /g' input.txt"},
		},
		{
			name: "python callable type subscript in fenced block",
			input: "```python\n" +
				"def register(fn: Callable[[dict[str, Any]], None]) -> None:\n" +
				"    handlers.append(fn)\n" +
				"```\n",
			wantInBody: []string{"def register(fn: Callable[[dict[str, Any]], None]) -> None:"},
		},
		{
			name:       "tilde fence protects code too",
			input:      "~~~bash\nif [[ -z \"$x\" ]]; then :; fi\n~~~\n",
			wantInBody: []string{"if [[ -z \"$x\" ]]; then :; fi"},
		},
		{
			name:       "inline code span protects code",
			input:      "Guard with `[[ -n \"$x\" ]]` before opening [[Real Note]].",
			wantLinks:  []string{"Real Note"},
			wantInBody: []string{"`[[ -n \"$x\" ]]`", "before opening Real Note."},
		},
		{
			name:       "fence indented inside a list item",
			input:      "- run the check:\n\n  ```bash\n  [[ -z \"$x\" ]] && exit 1\n  ```\n",
			wantInBody: []string{"[[ -z \"$x\" ]] && exit 1"},
		},
		{
			// Deeper than CommonMark's three-space cap, which Obsidian
			// notes reach routinely by nesting fences under list items.
			name: "fence indented five spaces",
			input: "1. step one\n" +
				"     ```python\n" +
				"     def register(fn: Callable[[dict[str, Any]], None]) -> None: ...\n" +
				"     ```\n",
			wantInBody: []string{"def register(fn: Callable[[dict[str, Any]], None]) -> None: ..."},
		},
		{
			name:       "genuine wikilink in prose",
			input:      "See [[Project Alpha]] for the rollout plan.",
			wantLinks:  []string{"Project Alpha"},
			wantInBody: []string{"See Project Alpha for the rollout plan."},
			notInBody:  []string{"[["},
		},
		{
			name:       "wikilink with display text",
			input:      "Read [[notes/target|the target note]] first.",
			wantLinks:  []string{"notes/target"},
			wantInBody: []string{"the target note (notes/target)"},
		},
		{
			name: "wikilink in prose and in code extracted once",
			input: "See [[Project Alpha]] for details.\n\n" +
				"```markdown\n" +
				"[[Project Alpha]]\n" +
				"```\n",
			wantLinks:  []string{"Project Alpha"},
			wantInBody: []string{"See Project Alpha for details.", "[[Project Alpha]]"},
		},
		{
			name: "unclosed fence does not eat the rest of the document",
			input: "```bash\n" +
				"echo hello\n\n" +
				"And then see [[Real Note]] for the follow-up.\n",
			wantLinks:  []string{"Real Note"},
			wantInBody: []string{"see Real Note for the follow-up."},
		},
		{
			name:       "embed in code preserved, embed in prose removed",
			input:      "```markdown\n![[diagram.png]]\n```\n\nInline: ![[real.png]] done.",
			wantInBody: []string{"![[diagram.png]]"},
			notInBody:  []string{"![[real.png]]"},
		},
		{
			name:      "empty fenced block",
			input:     "```\n```\n\nSee [[Real Note]].",
			wantLinks: []string{"Real Note"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := ParseMarkdown(tt.input, "note.md")

			if !equalStrings(doc.Links, tt.wantLinks) {
				t.Errorf("Links = %q, want %q", doc.Links, tt.wantLinks)
			}
			for _, want := range tt.wantInBody {
				if !strings.Contains(doc.BodyText, want) {
					t.Errorf("BodyText missing %q\nbody:\n%s", want, doc.BodyText)
				}
			}
			for _, unwanted := range tt.notInBody {
				if strings.Contains(doc.BodyText, unwanted) {
					t.Errorf("BodyText should not contain %q\nbody:\n%s", unwanted, doc.BodyText)
				}
			}
		})
	}
}

// TestParseMarkdownCodePreservedVerbatim checks that a fenced block survives
// parsing byte for byte, fences included.
func TestParseMarkdownCodePreservedVerbatim(t *testing.T) {
	block := "```bash\n" +
		"latest=$(git describe --tags --abbrev=0 2>/dev/null)\n" +
		"if [[ -z \"$latest\" ]]; then\n" +
		"  echo \"none\" | sed 's/[[:space:]]\\+//g'\n" +
		"fi\n" +
		"```"
	input := "Release helper.\n\n" + block + "\n\nSee [[Release Notes]].\n"

	doc := ParseMarkdown(input, "release.md")

	if !strings.Contains(doc.BodyText, block) {
		t.Errorf("fenced block was not preserved verbatim\nwant:\n%s\ngot body:\n%s", block, doc.BodyText)
	}
	if !equalStrings(doc.Links, []string{"Release Notes"}) {
		t.Errorf("Links = %q, want [Release Notes]", doc.Links)
	}
}

// TestExtractTagsIgnoresCode guards the pre-existing behaviour that inline
// tags are not harvested from code.
func TestExtractTagsIgnoresCode(t *testing.T) {
	input := "A #real-tag here.\n\n```bash\n# not-a-tag\necho '#also-not'\n```\n"

	doc := ParseMarkdown(input, "tags.md")

	var haveReal bool
	for _, tag := range doc.Tags {
		switch tag {
		case "real-tag":
			haveReal = true
		case "not-a-tag", "also-not":
			t.Errorf("tag %q extracted from code", tag)
		}
	}
	if !haveReal {
		t.Errorf("Tags = %q, want to include real-tag", doc.Tags)
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestParseMarkdownFrontmatterLinks(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		wantLinks     []string
		wantPropLinks map[string][]string
	}{
		{
			name:      "related list",
			input:     "---\nrelated:\n  - \"[[Atlas AWS Referenzarchitektur]]\"\n  - \"[[Prozessdiagramm Indexierung]]\"\n---\n\nBody with no links.\n",
			wantLinks: []string{"Atlas AWS Referenzarchitektur", "Prozessdiagramm Indexierung"},
			wantPropLinks: map[string][]string{
				"related": {"Atlas AWS Referenzarchitektur", "Prozessdiagramm Indexierung"},
			},
		},
		{
			name:      "single string property",
			input:     "---\nparent: \"[[Info Platform]]\"\n---\n\nBody.\n",
			wantLinks: []string{"Info Platform"},
			wantPropLinks: map[string][]string{
				"parent": {"Info Platform"},
			},
		},
		{
			name:      "display text keeps only the target",
			input:     "---\nauthor: \"[[Gene Kim|Gene]]\"\n---\n\nBody.\n",
			wantLinks: []string{"Gene Kim"},
			wantPropLinks: map[string][]string{
				"author": {"Gene Kim"},
			},
		},
		{
			name:      "body and frontmatter links merge, body first",
			input:     "---\nrelated:\n  - \"[[From Frontmatter]]\"\n---\n\nSee [[From Body]].\n",
			wantLinks: []string{"From Body", "From Frontmatter"},
			wantPropLinks: map[string][]string{
				"related": {"From Frontmatter"},
			},
		},
		{
			name:          "duplicate across body and frontmatter appears once",
			input:         "---\nrelated:\n  - \"[[Shared Note]]\"\n---\n\nSee [[Shared Note]].\n",
			wantLinks:     []string{"Shared Note"},
			wantPropLinks: map[string][]string{"related": {"Shared Note"}},
		},
		{
			name:          "properties without wikilinks are ignored",
			input:         "---\ntags:\n  - copebit\nstatus: draft\ncount: 3\n---\n\nBody.\n",
			wantLinks:     nil,
			wantPropLinks: nil,
		},
		{
			name:          "multiple properties are ordered by property name",
			input:         "---\nzeta: \"[[Z Note]]\"\nalpha: \"[[A Note]]\"\n---\n\nBody.\n",
			wantLinks:     []string{"A Note", "Z Note"},
			wantPropLinks: map[string][]string{"alpha": {"A Note"}, "zeta": {"Z Note"}},
		},
		{
			name:          "nested map value is walked",
			input:         "---\nmeta:\n  source: \"[[Nested Note]]\"\n---\n\nBody.\n",
			wantLinks:     []string{"Nested Note"},
			wantPropLinks: map[string][]string{"meta": {"Nested Note"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := ParseMarkdown(tt.input, "note.md")
			if !reflect.DeepEqual(doc.Links, tt.wantLinks) {
				t.Errorf("Links = %#v, want %#v", doc.Links, tt.wantLinks)
			}
			if !reflect.DeepEqual(doc.PropertyLinks, tt.wantPropLinks) {
				t.Errorf("PropertyLinks = %#v, want %#v", doc.PropertyLinks, tt.wantPropLinks)
			}
		})
	}
}

// A wikilink in frontmatter must not survive in the indexed body: the whole
// frontmatter block is stripped before chunking.
func TestParseMarkdownFrontmatterNotInBody(t *testing.T) {
	doc := ParseMarkdown("---\nrelated:\n  - \"[[Other Note]]\"\n---\n\nJust prose.\n", "note.md")
	if strings.Contains(doc.BodyText, "Other Note") {
		t.Errorf("frontmatter leaked into body: %q", doc.BodyText)
	}
	if doc.BodyText != "Just prose." {
		t.Errorf("BodyText = %q, want %q", doc.BodyText, "Just prose.")
	}
}
