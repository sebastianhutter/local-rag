package parser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetCodeLanguage(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"main.py", "python"},
		{"main.go", "go"},
		{"main.tf", "hcl"},
		{"app.ts", "typescript"},
		{"app.tsx", "tsx"},
		{"app.js", "javascript"},
		{"lib.rs", "rust"},
		{"Main.java", "java"},
		{"util.c", "c"},
		{"util.cpp", "cpp"},
		{"Program.cs", "csharp"},
		{"script.sh", "bash"},
		{"config.yaml", "yaml"},
		{"data.json", "plaintext"},
		{"Dockerfile", "dockerfile"},
		{"unknown.xyz", ""},
	}

	for _, tt := range tests {
		got := GetCodeLanguage(tt.path)
		if got != tt.want {
			t.Errorf("GetCodeLanguage(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestIsCodeFile(t *testing.T) {
	if !IsCodeFile("main.py") {
		t.Error("expected main.py to be a code file")
	}
	if !IsCodeFile("Dockerfile") {
		t.Error("expected Dockerfile to be a code file")
	}
	if IsCodeFile("photo.png") {
		t.Error("expected photo.png to not be a code file")
	}
}

func TestParseCodeFilePython(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "example.py")
	source := `import os

def hello(name):
    print(f"Hello, {name}!")

class Greeter:
    def greet(self):
        pass

x = 42
`
	os.WriteFile(path, []byte(source), 0644)

	doc := ParseCodeFile(path, "python", "example.py", 500, 50)
	if doc == nil {
		t.Fatal("ParseCodeFile returned nil")
	}

	if doc.Language != "python" {
		t.Errorf("Language = %q, want %q", doc.Language, "python")
	}

	// Should have: top-level (import + x=42), hello function, Greeter class.
	if len(doc.Blocks) < 2 {
		t.Errorf("Expected at least 2 blocks, got %d", len(doc.Blocks))
	}

	// Check that we found the function.
	foundHello := false
	foundGreeter := false
	for _, b := range doc.Blocks {
		if b.SymbolName == "hello" && b.SymbolType == "function" {
			foundHello = true
		}
		if b.SymbolName == "Greeter" && b.SymbolType == "class" {
			foundGreeter = true
		}
	}
	if !foundHello {
		t.Error("expected to find 'hello' function block")
	}
	if !foundGreeter {
		t.Error("expected to find 'Greeter' class block")
	}
}

func TestParseCodeFileGo(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "example.go")
	source := `package main

import "fmt"

func main() {
	fmt.Println("hello")
}

type Config struct {
	Name string
}
`
	os.WriteFile(path, []byte(source), 0644)

	doc := ParseCodeFile(path, "go", "example.go", 500, 50)
	if doc == nil {
		t.Fatal("ParseCodeFile returned nil")
	}

	foundMain := false
	foundConfig := false
	for _, b := range doc.Blocks {
		if b.SymbolName == "main" && b.SymbolType == "function" {
			foundMain = true
		}
		if b.SymbolName == "Config" && b.SymbolType == "type" {
			foundConfig = true
		}
	}
	if !foundMain {
		t.Error("expected to find 'main' function block")
	}
	if !foundConfig {
		t.Error("expected to find 'Config' type block")
	}
}

func TestParseCodeFileHCL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.tf")
	source := `resource "aws_instance" "web" {
  ami           = "ami-12345"
  instance_type = "t3.micro"
}

variable "region" {
  default = "us-east-1"
}
`
	os.WriteFile(path, []byte(source), 0644)

	doc := ParseCodeFile(path, "hcl", "main.tf", 500, 50)
	if doc == nil {
		t.Fatal("ParseCodeFile returned nil")
	}

	if len(doc.Blocks) < 2 {
		t.Errorf("Expected at least 2 blocks, got %d", len(doc.Blocks))
	}

	foundResource := false
	foundVariable := false
	for _, b := range doc.Blocks {
		if b.SymbolName == "resource aws_instance web" {
			foundResource = true
		}
		if b.SymbolName == "variable region" {
			foundVariable = true
		}
	}
	if !foundResource {
		t.Error("expected to find 'resource aws_instance web' block")
	}
	if !foundVariable {
		t.Error("expected to find 'variable region' block")
	}
}

func TestParseCodeFilePlaintext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.txt")
	os.WriteFile(path, []byte("some data\nmore data\n"), 0644)

	doc := ParseCodeFile(path, "plaintext", "data.txt", 500, 50)
	if doc == nil {
		t.Fatal("ParseCodeFile returned nil")
	}
	if len(doc.Blocks) != 1 {
		t.Errorf("Expected 1 block, got %d", len(doc.Blocks))
	}
	if doc.Blocks[0].SymbolType != "module_top" {
		t.Errorf("Expected module_top, got %q", doc.Blocks[0].SymbolType)
	}
}

func TestParseCodeFileNonexistent(t *testing.T) {
	doc := ParseCodeFile("/nonexistent/file.py", "python", "file.py", 500, 50)
	if doc != nil {
		t.Error("expected nil for nonexistent file")
	}
}

// TestParseCodeFileOversizedClassSplits verifies cAST behavior: a class larger
// than the budget is split into per-method blocks that carry the class name as
// their SymbolPath, instead of being word-window split across method boundaries.
func TestParseCodeFileOversizedClassSplits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "svc.py")

	var sb strings.Builder
	sb.WriteString("class OrderService:\n")
	for _, name := range []string{"create", "cancel", "refund"} {
		sb.WriteString("    def " + name + "(self, order):\n")
		for i := 0; i < 4; i++ {
			sb.WriteString("        x = compute(order)\n")
		}
		sb.WriteString("\n")
	}
	os.WriteFile(path, []byte(sb.String()), 0644)

	// Budget fits a single method (~15 words) but not the whole class (~50),
	// so the class splits along method boundaries.
	doc := ParseCodeFile(path, "python", "svc.py", 30, 5)
	if doc == nil {
		t.Fatal("ParseCodeFile returned nil")
	}

	methods := map[string]bool{}
	for _, b := range doc.Blocks {
		if b.SymbolType == "function" && b.SymbolPath == "OrderService" {
			methods[b.SymbolName] = true
		}
	}
	for _, want := range []string{"create", "cancel", "refund"} {
		if !methods[want] {
			t.Errorf("expected method %q split out with SymbolPath 'OrderService'; blocks=%+v", want, blockSummaries(doc))
		}
	}
}

// TestParseCodeFileMergesSmallTrivia verifies that a run of small top-level
// statements is merged into few module_top blocks rather than one per statement.
func TestParseCodeFileMergesSmallTrivia(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "consts.py")

	var sb strings.Builder
	for i := 0; i < 20; i++ {
		sb.WriteString("A" + string(rune('A'+i)) + " = " + "1\n")
	}
	os.WriteFile(path, []byte(sb.String()), 0644)

	doc := ParseCodeFile(path, "python", "consts.py", 500, 50)
	if doc == nil {
		t.Fatal("ParseCodeFile returned nil")
	}
	// 20 tiny assignments (~60 words) fit one budget → a single merged block.
	if len(doc.Blocks) != 1 {
		t.Errorf("expected small trivia merged into 1 block, got %d", len(doc.Blocks))
	}
	if doc.Blocks[0].SymbolType != "module_top" {
		t.Errorf("expected module_top, got %q", doc.Blocks[0].SymbolType)
	}
}

// TestParseCodeFilePlaintextSplits verifies large plaintext is split by budget.
func TestParseCodeFilePlaintextSplits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.txt")
	var sb strings.Builder
	for i := 0; i < 300; i++ {
		sb.WriteString("word ")
	}
	os.WriteFile(path, []byte(sb.String()), 0644)

	doc := ParseCodeFile(path, "plaintext", "big.txt", 100, 10)
	if doc == nil {
		t.Fatal("ParseCodeFile returned nil")
	}
	if len(doc.Blocks) < 2 {
		t.Errorf("expected large plaintext split into multiple blocks, got %d", len(doc.Blocks))
	}
}

func blockSummaries(doc *CodeDocument) []string {
	var out []string
	for _, b := range doc.Blocks {
		out = append(out, b.SymbolType+":"+b.SymbolPath+"/"+b.SymbolName)
	}
	return out
}

func TestNodeSymbolType(t *testing.T) {
	tests := []struct {
		nodeType string
		want     string
	}{
		{"function_declaration", "function"},
		{"class_definition", "class"},
		{"method_declaration", "method"},
		{"block", "block"},
		{"unknown_type", "block"},
	}
	for _, tt := range tests {
		got := nodeSymbolType(tt.nodeType)
		if got != tt.want {
			t.Errorf("nodeSymbolType(%q) = %q, want %q", tt.nodeType, got, tt.want)
		}
	}
}
