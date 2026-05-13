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
