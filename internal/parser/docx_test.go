package parser

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func createTestDocx(t *testing.T, documentXML string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.docx")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	w, err := zw.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(documentXML)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// createTestDocxWithPart builds a package whose main document part is named
// partName and is addressed through _rels/.rels, as Word does for repaired or
// converted documents. relTarget is written verbatim so the "/word/…" form can
// be exercised too.
func createTestDocxWithPart(t *testing.T, partName, relTarget, documentXML string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.docx")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)

	rels := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/>
  <Relationship Id="rId1" Type="` + mainPartRelType + `" Target="` + relTarget + `"/>
</Relationships>`

	for name, content := range map[string]string{
		"_rels/.rels": rels,
		partName:      documentXML,
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

const testBodyXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body><w:p><w:r><w:t>Repaired document text</w:t></w:r></w:p></w:body>
</w:document>`

// Word names the main part word/document2.xml (or document22.xml, …) after
// repairing or converting a file. The part is addressed through the package
// relationships, so assuming the conventional name silently drops these.
func TestParseDocx_MainPartFromRelationships(t *testing.T) {
	tests := []struct {
		name      string
		partName  string
		relTarget string
	}{
		{"relative target", "word/document2.xml", "word/document2.xml"},
		{"absolute target", "word/document22.xml", "/word/document22.xml"},
		{"dot-slash target", "word/document2.xml", "./word/document2.xml"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := ParseDocx(createTestDocxWithPart(t, tt.partName, tt.relTarget, testBodyXML))
			if doc.Text != "Repaired document text" {
				t.Errorf("got %q, want the document body", doc.Text)
			}
		})
	}
}

// A package with no usable _rels/.rels still parses via the conventional name.
func TestParseDocx_FallsBackToConventionalPart(t *testing.T) {
	doc := ParseDocx(createTestDocx(t, testBodyXML))
	if doc.Text != "Repaired document text" {
		t.Errorf("got %q, want the document body", doc.Text)
	}
}

// A relationship pointing at a part that is not in the archive must report the
// name it looked for, not the conventional one.
func TestParseDocx_MissingTargetPart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broken.docx")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create("_rels/.rels")
	w.Write([]byte(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
	  <Relationship Id="rId1" Type="` + mainPartRelType + `" Target="word/nowhere.xml"/></Relationships>`))
	zw.Close()
	f.Close()

	if doc := ParseDocx(path); doc.Text != "" {
		t.Errorf("got %q, want empty text", doc.Text)
	}

	if _, err := extractDocxText(path); err == nil || !strings.Contains(err.Error(), "word/nowhere.xml") {
		t.Errorf("error should name the part it looked for, got %v", err)
	}
}

func TestParseDocx_BasicText(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p><w:r><w:t>Hello World</w:t></w:r></w:p>
  </w:body>
</w:document>`

	doc := ParseDocx(createTestDocx(t, xml))
	if doc.Text != "Hello World" {
		t.Errorf("got %q, want %q", doc.Text, "Hello World")
	}
}

func TestParseDocx_ParagraphsWithAttributes(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p w:rsidR="00A77427" w:rsidRDefault="007A3403">
      <w:r><w:t>First paragraph</w:t></w:r>
    </w:p>
    <w:p w:rsidR="00A77427" w:rsidRPr="0091588A" w:rsidRDefault="007A3403">
      <w:r w:rsidRPr="0091588A"><w:t>Second paragraph</w:t></w:r>
    </w:p>
  </w:body>
</w:document>`

	doc := ParseDocx(createTestDocx(t, xml))
	if !strings.Contains(doc.Text, "First paragraph") {
		t.Errorf("missing 'First paragraph' in %q", doc.Text)
	}
	if !strings.Contains(doc.Text, "Second paragraph") {
		t.Errorf("missing 'Second paragraph' in %q", doc.Text)
	}
	if !strings.Contains(doc.Text, "\n") {
		t.Error("paragraphs should be separated by newlines")
	}
}

func TestParseDocx_MultipleRunsInParagraph(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p w:rsidR="001">
      <w:r><w:t>Hello </w:t></w:r>
      <w:r w:rsidRPr="002"><w:t>World</w:t></w:r>
    </w:p>
  </w:body>
</w:document>`

	doc := ParseDocx(createTestDocx(t, xml))
	if doc.Text != "Hello World" {
		t.Errorf("got %q, want %q", doc.Text, "Hello World")
	}
}

func TestParseDocx_PreservedSpaces(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p>
      <w:r><w:t xml:space="preserve">Hello </w:t></w:r>
      <w:r><w:t xml:space="preserve"> World</w:t></w:r>
    </w:p>
  </w:body>
</w:document>`

	doc := ParseDocx(createTestDocx(t, xml))
	if doc.Text != "Hello  World" {
		t.Errorf("got %q, want %q", doc.Text, "Hello  World")
	}
}

func TestParseDocx_EmptyDocument(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body></w:body>
</w:document>`

	doc := ParseDocx(createTestDocx(t, xml))
	if doc.Text != "" {
		t.Errorf("got %q, want empty string", doc.Text)
	}
}

func TestParseDocx_InvalidFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.docx")
	if err := os.WriteFile(path, []byte("not a zip file"), 0644); err != nil {
		t.Fatal(err)
	}
	doc := ParseDocx(path)
	if doc.Text != "" {
		t.Errorf("got %q, want empty string for invalid file", doc.Text)
	}
}
