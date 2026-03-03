package parser

import (
	"os"
	"path/filepath"
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

	doc := ParseCodeFile(path, "python", "example.py")
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

	doc := ParseCodeFile(path, "go", "example.go")
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

	doc := ParseCodeFile(path, "hcl", "main.tf")
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

	doc := ParseCodeFile(path, "plaintext", "data.txt")
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
	doc := ParseCodeFile("/nonexistent/file.py", "python", "file.py")
	if doc != nil {
		t.Error("expected nil for nonexistent file")
	}
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
