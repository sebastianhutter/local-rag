package parser

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/bash"
	"github.com/smacker/go-tree-sitter/c"
	"github.com/smacker/go-tree-sitter/cpp"
	"github.com/smacker/go-tree-sitter/csharp"
	"github.com/smacker/go-tree-sitter/css"
	"github.com/smacker/go-tree-sitter/dockerfile"
	tshtml "github.com/smacker/go-tree-sitter/html"
	"github.com/smacker/go-tree-sitter/java"
	"github.com/smacker/go-tree-sitter/javascript"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/smacker/go-tree-sitter/ruby"
	"github.com/smacker/go-tree-sitter/rust"
	"github.com/smacker/go-tree-sitter/sql"
	"github.com/smacker/go-tree-sitter/toml"
	tsgo "github.com/smacker/go-tree-sitter/golang"
	tshcl "github.com/smacker/go-tree-sitter/hcl"
	tsmarkdown "github.com/smacker/go-tree-sitter/markdown/tree-sitter-markdown"
	"github.com/smacker/go-tree-sitter/typescript/tsx"
	tsts "github.com/smacker/go-tree-sitter/typescript/typescript"
	"github.com/smacker/go-tree-sitter/yaml"
)

// CodeBlock is a structural block of source code.
type CodeBlock struct {
	Text       string
	Language   string
	SymbolName string
	SymbolType string // "function", "class", "method", "block", "module_top"
	StartLine  int    // 1-based
	EndLine    int    // 1-based
	FilePath   string // relative path within repo
}

// CodeDocument is a parsed code file containing structural blocks.
type CodeDocument struct {
	FilePath string
	Language string
	Blocks   []CodeBlock
}

// CodeExtensionMap maps file extensions to tree-sitter language names.
var CodeExtensionMap = map[string]string{
	".py":     "python",
	".go":     "go",
	".tf":     "hcl",
	".tfvars": "hcl",
	".hcl":    "hcl",
	".ts":     "typescript",
	".tsx":    "tsx",
	".js":     "javascript",
	".jsx":    "javascript",
	".rs":     "rust",
	".java":   "java",
	".c":      "c",
	".h":      "c",
	".cpp":    "cpp",
	".cc":     "cpp",
	".hpp":    "cpp",
	".cs":     "csharp",
	".rb":     "ruby",
	".sh":     "bash",
	".bash":   "bash",
	".yaml":   "yaml",
	".yml":    "yaml",
	".json":   "plaintext",
	".txt":    "plaintext",
	".csv":    "plaintext",
	".md":     "markdown",
	".rst":    "plaintext",
	".toml":   "toml",
	".sql":    "sql",
	".xml":    "plaintext",
	".html":   "html",
	".htm":    "html",
	".css":    "css",
	".scss":   "plaintext",
}

// CodeFilenameMap maps specific filenames to languages.
var CodeFilenameMap = map[string]string{
	"Dockerfile": "dockerfile",
	"Makefile":   "plaintext",
}

// splitNodeTypes defines which node types trigger structural splitting per language.
var splitNodeTypes = map[string]map[string]bool{
	"python":     {"function_definition": true, "class_definition": true, "decorated_definition": true},
	"go":         {"function_declaration": true, "method_declaration": true, "type_declaration": true},
	"hcl":        {"block": true},
	"typescript":  {"function_declaration": true, "class_declaration": true, "export_statement": true, "interface_declaration": true, "type_alias_declaration": true, "enum_declaration": true},
	"tsx":         {"function_declaration": true, "class_declaration": true, "export_statement": true, "interface_declaration": true, "type_alias_declaration": true, "enum_declaration": true},
	"javascript":  {"function_declaration": true, "class_declaration": true, "export_statement": true},
	"rust":        {"function_item": true, "struct_item": true, "enum_item": true, "impl_item": true, "trait_item": true, "mod_item": true},
	"java":        {"class_declaration": true, "interface_declaration": true, "enum_declaration": true, "method_declaration": true},
	"c":           {"function_definition": true, "struct_specifier": true, "enum_specifier": true},
	"cpp":         {"function_definition": true, "class_specifier": true, "struct_specifier": true, "enum_specifier": true},
	"csharp":      {"class_declaration": true, "interface_declaration": true, "method_declaration": true, "enum_declaration": true},
	"ruby":        {"method": true, "class": true, "module": true},
	"bash":        {"function_definition": true},
}

// languageMap maps language names to tree-sitter Language objects.
var languageMap = map[string]*sitter.Language{
	"python":     python.GetLanguage(),
	"go":         tsgo.GetLanguage(),
	"hcl":        tshcl.GetLanguage(),
	"typescript": tsts.GetLanguage(),
	"tsx":        tsx.GetLanguage(),
	"javascript": javascript.GetLanguage(),
	"rust":       rust.GetLanguage(),
	"java":       java.GetLanguage(),
	"c":          c.GetLanguage(),
	"cpp":        cpp.GetLanguage(),
	"csharp":     csharp.GetLanguage(),
	"ruby":       ruby.GetLanguage(),
	"bash":       bash.GetLanguage(),
	"yaml":       yaml.GetLanguage(),
	"toml":       toml.GetLanguage(),
	"sql":        sql.GetLanguage(),
	"html":       tshtml.GetLanguage(),
	"css":        css.GetLanguage(),
	"dockerfile": dockerfile.GetLanguage(),
	"markdown":   tsmarkdown.GetLanguage(),
}

// GetSupportedCodeExtensions returns all file extensions supported by the code parser.
func GetSupportedCodeExtensions() map[string]bool {
	result := make(map[string]bool, len(CodeExtensionMap))
	for ext := range CodeExtensionMap {
		result[ext] = true
	}
	return result
}

// IsCodeFile checks if a file is a supported code file.
func IsCodeFile(path string) bool {
	name := filepath.Base(path)
	if _, ok := CodeFilenameMap[name]; ok {
		return true
	}
	ext := strings.ToLower(filepath.Ext(path))
	_, ok := CodeExtensionMap[ext]
	return ok
}

// GetCodeLanguage returns the tree-sitter language name for a file path.
func GetCodeLanguage(path string) string {
	name := filepath.Base(path)
	if lang, ok := CodeFilenameMap[name]; ok {
		return lang
	}
	ext := strings.ToLower(filepath.Ext(path))
	return CodeExtensionMap[ext]
}

// ParseCodeFile parses a source code file into structural blocks using tree-sitter.
func ParseCodeFile(filePath, language, relativePath string) *CodeDocument {
	sourceBytes, err := os.ReadFile(filePath)
	if err != nil {
		slog.Error("cannot read file", "path", filePath, "err", err)
		return nil
	}

	// Plaintext files have no tree-sitter grammar.
	if language == "plaintext" {
		return plainTextDoc(sourceBytes, language, relativePath)
	}

	lang, ok := languageMap[language]
	if !ok {
		slog.Warn("unsupported tree-sitter language, treating as plaintext", "language", language)
		return plainTextDoc(sourceBytes, language, relativePath)
	}

	parser := sitter.NewParser()
	parser.SetLanguage(lang)

	tree, err := parser.ParseCtx(context.Background(), nil, sourceBytes)
	if err != nil {
		slog.Error("failed to parse", "path", filePath, "err", err)
		return nil
	}

	root := tree.RootNode()
	splits := splitNodeTypes[language]

	doc := &CodeDocument{FilePath: relativePath, Language: language}

	// For HCL, the root has a "body" child that contains the actual blocks.
	children := collectChildren(root)
	if language == "hcl" && len(children) == 1 && children[0].Type() == "body" {
		children = collectChildren(children[0])
	}

	// Accumulate non-split nodes into module_top blocks.
	var topLines []string
	topStartLine := -1
	topEndLine := -1

	flushTopLevel := func() {
		if len(topLines) > 0 && topStartLine >= 0 {
			text := strings.Join(topLines, "\n")
			if strings.TrimSpace(text) != "" {
				doc.Blocks = append(doc.Blocks, CodeBlock{
					Text:       text,
					Language:   language,
					SymbolName: "(top-level)",
					SymbolType: "module_top",
					StartLine:  topStartLine,
					EndLine:    topEndLine,
					FilePath:   relativePath,
				})
			}
		}
		topLines = nil
		topStartLine = -1
		topEndLine = -1
	}

	for _, child := range children {
		nodeType := child.Type()
		if splits[nodeType] {
			flushTopLevel()

			nodeText := child.Content(sourceBytes)
			startLine := int(child.StartPoint().Row) + 1
			endLine := int(child.EndPoint().Row) + 1

			symbolName := extractSymbolName(child, language, sourceBytes)
			symbolType := nodeSymbolType(nodeType)

			// For decorated definitions, refine based on inner node.
			if symbolType == "decorated" && language == "python" {
				for i := 0; i < int(child.ChildCount()); i++ {
					inner := child.Child(i)
					if inner.Type() == "class_definition" {
						symbolType = "class"
						break
					} else if inner.Type() == "function_definition" {
						symbolType = "function"
						break
					}
				}
			}

			doc.Blocks = append(doc.Blocks, CodeBlock{
				Text:       nodeText,
				Language:   language,
				SymbolName: symbolName,
				SymbolType: symbolType,
				StartLine:  startLine,
				EndLine:    endLine,
				FilePath:   relativePath,
			})
		} else {
			nodeText := child.Content(sourceBytes)
			startLine := int(child.StartPoint().Row) + 1
			endLine := int(child.EndPoint().Row) + 1
			if topStartLine < 0 {
				topStartLine = startLine
			}
			topEndLine = endLine
			topLines = append(topLines, nodeText)
		}
	}

	flushTopLevel()

	// If no structural blocks found, treat entire file as one block.
	if len(doc.Blocks) == 0 {
		return plainTextDoc(sourceBytes, language, relativePath)
	}

	return doc
}

func plainTextDoc(sourceBytes []byte, language, relativePath string) *CodeDocument {
	fullText := string(sourceBytes)
	doc := &CodeDocument{FilePath: relativePath, Language: language}
	if strings.TrimSpace(fullText) != "" {
		lineCount := strings.Count(fullText, "\n") + 1
		doc.Blocks = append(doc.Blocks, CodeBlock{
			Text:       fullText,
			Language:   language,
			SymbolName: "(file)",
			SymbolType: "module_top",
			StartLine:  1,
			EndLine:    lineCount,
			FilePath:   relativePath,
		})
	}
	return doc
}

func collectChildren(node *sitter.Node) []*sitter.Node {
	count := int(node.ChildCount())
	children := make([]*sitter.Node, 0, count)
	for i := 0; i < count; i++ {
		children = append(children, node.Child(i))
	}
	return children
}

func extractSymbolName(node *sitter.Node, language string, sourceBytes []byte) string {
	switch language {
	case "hcl":
		return extractHCLSymbol(node, sourceBytes)
	case "python":
		return extractPythonSymbol(node, sourceBytes)
	case "go":
		return extractGoSymbol(node, sourceBytes)
	case "typescript", "tsx", "javascript":
		return extractJSSymbol(node, language, sourceBytes)
	case "rust":
		return extractIdentifierChild(node, sourceBytes, "identifier", "type_identifier")
	case "java", "csharp":
		return extractIdentifierChild(node, sourceBytes, "identifier")
	case "ruby":
		return extractIdentifierChild(node, sourceBytes, "identifier", "constant")
	case "c", "cpp":
		return extractCSymbol(node, sourceBytes)
	default:
		return extractIdentifierChild(node, sourceBytes, "identifier")
	}
}

func extractHCLSymbol(node *sitter.Node, sourceBytes []byte) string {
	var parts []string
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		t := child.Type()
		if t == "identifier" {
			parts = append(parts, child.Content(sourceBytes))
		} else if t == "string_lit" {
			text := strings.Trim(child.Content(sourceBytes), `"`)
			parts = append(parts, text)
		} else if t == "block" || t == "{" {
			break
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, " ")
	}
	return node.Type()
}

func extractPythonSymbol(node *sitter.Node, sourceBytes []byte) string {
	target := node
	if node.Type() == "decorated_definition" {
		for i := 0; i < int(node.ChildCount()); i++ {
			child := node.Child(i)
			if child.Type() == "function_definition" || child.Type() == "class_definition" {
				target = child
				break
			}
		}
	}
	for i := 0; i < int(target.ChildCount()); i++ {
		child := target.Child(i)
		if child.Type() == "identifier" {
			return child.Content(sourceBytes)
		}
	}
	return target.Type()
}

func extractGoSymbol(node *sitter.Node, sourceBytes []byte) string {
	if node.Type() == "type_declaration" {
		for i := 0; i < int(node.ChildCount()); i++ {
			child := node.Child(i)
			if child.Type() == "type_spec" {
				for j := 0; j < int(child.ChildCount()); j++ {
					gc := child.Child(j)
					if gc.Type() == "type_identifier" {
						return gc.Content(sourceBytes)
					}
				}
			}
		}
	}
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "identifier" || child.Type() == "field_identifier" {
			return child.Content(sourceBytes)
		}
	}
	return node.Type()
}

func extractJSSymbol(node *sitter.Node, language string, sourceBytes []byte) string {
	if node.Type() == "export_statement" {
		for i := 0; i < int(node.ChildCount()); i++ {
			child := node.Child(i)
			switch child.Type() {
			case "function_declaration", "class_declaration", "interface_declaration",
				"type_alias_declaration", "enum_declaration":
				return extractJSSymbol(child, language, sourceBytes)
			case "lexical_declaration":
				for j := 0; j < int(child.ChildCount()); j++ {
					gc := child.Child(j)
					if gc.Type() == "variable_declarator" {
						for k := 0; k < int(gc.ChildCount()); k++ {
							ggc := gc.Child(k)
							if ggc.Type() == "identifier" {
								return ggc.Content(sourceBytes)
							}
						}
					}
				}
			}
		}
		return "export"
	}
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "identifier" || child.Type() == "type_identifier" {
			return child.Content(sourceBytes)
		}
	}
	return node.Type()
}

func extractCSymbol(node *sitter.Node, sourceBytes []byte) string {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "identifier" || child.Type() == "type_identifier" {
			return child.Content(sourceBytes)
		}
		if strings.Contains(child.Type(), "declarator") {
			for j := 0; j < int(child.ChildCount()); j++ {
				gc := child.Child(j)
				if gc.Type() == "identifier" || gc.Type() == "field_identifier" {
					return gc.Content(sourceBytes)
				}
				if strings.Contains(gc.Type(), "declarator") {
					for k := 0; k < int(gc.ChildCount()); k++ {
						ggc := gc.Child(k)
						if ggc.Type() == "identifier" || ggc.Type() == "field_identifier" {
							return ggc.Content(sourceBytes)
						}
					}
				}
			}
		}
	}
	return node.Type()
}

func extractIdentifierChild(node *sitter.Node, sourceBytes []byte, types ...string) string {
	typeSet := make(map[string]bool, len(types))
	for _, t := range types {
		typeSet[t] = true
	}
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if typeSet[child.Type()] {
			return child.Content(sourceBytes)
		}
	}
	return node.Type()
}

func nodeSymbolType(nodeType string) string {
	typeMap := map[string]string{
		"function_definition":    "function",
		"function_declaration":   "function",
		"function_item":          "function",
		"method_declaration":     "method",
		"method":                 "method",
		"class_definition":       "class",
		"class_declaration":      "class",
		"class_specifier":        "class",
		"decorated_definition":   "decorated",
		"type_declaration":       "type",
		"type_alias_declaration": "type",
		"interface_declaration":  "interface",
		"enum_declaration":       "enum",
		"enum_specifier":         "enum",
		"enum_item":              "enum",
		"struct_item":            "struct",
		"struct_specifier":       "struct",
		"impl_item":              "impl",
		"trait_item":             "trait",
		"mod_item":               "module",
		"module":                 "module",
		"export_statement":       "export",
		"block":                  "block", // HCL
	}
	if t, ok := typeMap[nodeType]; ok {
		return t
	}
	return "block"
}
