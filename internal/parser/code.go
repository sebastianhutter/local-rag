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
	tsgo "github.com/smacker/go-tree-sitter/golang"
	tshcl "github.com/smacker/go-tree-sitter/hcl"
	tshtml "github.com/smacker/go-tree-sitter/html"
	"github.com/smacker/go-tree-sitter/java"
	"github.com/smacker/go-tree-sitter/javascript"
	tsmarkdown "github.com/smacker/go-tree-sitter/markdown/tree-sitter-markdown"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/smacker/go-tree-sitter/ruby"
	"github.com/smacker/go-tree-sitter/rust"
	"github.com/smacker/go-tree-sitter/sql"
	"github.com/smacker/go-tree-sitter/toml"
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
	SymbolPath string // enclosing symbol chain, e.g. "OrderService" for a method
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
	"typescript": {"function_declaration": true, "class_declaration": true, "export_statement": true, "interface_declaration": true, "type_alias_declaration": true, "enum_declaration": true},
	"tsx":        {"function_declaration": true, "class_declaration": true, "export_statement": true, "interface_declaration": true, "type_alias_declaration": true, "enum_declaration": true},
	"javascript": {"function_declaration": true, "class_declaration": true, "export_statement": true},
	"rust":       {"function_item": true, "struct_item": true, "enum_item": true, "impl_item": true, "trait_item": true, "mod_item": true},
	"java":       {"class_declaration": true, "interface_declaration": true, "enum_declaration": true, "method_declaration": true},
	"c":          {"function_definition": true, "struct_specifier": true, "enum_specifier": true},
	"cpp":        {"function_definition": true, "class_specifier": true, "struct_specifier": true, "enum_specifier": true},
	"csharp":     {"class_declaration": true, "interface_declaration": true, "method_declaration": true, "enum_declaration": true},
	"ruby":       {"method": true, "class": true, "module": true},
	"bash":       {"function_definition": true},
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

// ParseCodeFile parses a source code file into structural, size-bounded blocks
// using tree-sitter. It follows the "cAST" split-then-merge strategy
// (arXiv:2506.15655): a named definition that fits maxWords becomes one block;
// an oversized definition is split recursively along its AST child boundaries
// (e.g. a large class → one block per method, each carrying the enclosing
// symbol path); runs of small adjacent trivia (imports, top-level statements)
// are greedily merged up to maxWords. Word-window splitting is only a last
// resort for oversized leaves that have no further structure.
//
// maxWords is the per-block size budget (whitespace-delimited words, matching
// the project-wide token proxy); overlap is the word overlap used by the
// last-resort window splitter. cAST measures budget in non-whitespace
// characters, but we use words for consistency with the other chunkers.
func ParseCodeFile(filePath, language, relativePath string, maxWords, overlap int) *CodeDocument {
	sourceBytes, err := os.ReadFile(filePath)
	if err != nil {
		slog.Error("cannot read file", "path", filePath, "err", err)
		return nil
	}

	// Plaintext files have no tree-sitter grammar.
	if language == "plaintext" {
		return plainTextDoc(sourceBytes, language, relativePath, maxWords, overlap)
	}

	lang, ok := languageMap[language]
	if !ok {
		slog.Warn("unsupported tree-sitter language, treating as plaintext", "language", language)
		return plainTextDoc(sourceBytes, language, relativePath, maxWords, overlap)
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

	ctx := &chunkCtx{
		src:      sourceBytes,
		language: language,
		relPath:  relativePath,
		splits:   splits,
		maxWords: maxWords,
		overlap:  overlap,
	}
	doc.Blocks = ctx.chunkNodes(children, "", true)

	// If no structural blocks found, treat entire file as plaintext.
	if len(doc.Blocks) == 0 {
		return plainTextDoc(sourceBytes, language, relativePath, maxWords, overlap)
	}

	return doc
}

// chunkCtx holds the invariants for a single file's recursive chunking.
type chunkCtx struct {
	src      []byte
	language string
	relPath  string
	splits   map[string]bool
	maxWords int
	overlap  int
}

// chunkNodes converts a sequence of sibling AST nodes into size-bounded blocks.
// parentPath is the enclosing symbol chain (empty at file scope). topLevel marks
// file-scope trivia as "module_top" (vs "block" when nested inside a definition).
func (c *chunkCtx) chunkNodes(nodes []*sitter.Node, parentPath string, topLevel bool) []CodeBlock {
	triviaType, triviaName := "block", "(block)"
	if topLevel {
		triviaType, triviaName = "module_top", "(top-level)"
	}

	var blocks []CodeBlock
	var buf []*sitter.Node
	bufWords := 0

	flush := func() {
		if len(buf) == 0 {
			return
		}
		if blk, ok := c.spanBlock(buf, parentPath, triviaName, triviaType); ok {
			blocks = append(blocks, blk)
		}
		buf = nil
		bufWords = 0
	}

	for _, node := range nodes {
		text := node.Content(c.src)
		w := wordCount(text)

		switch {
		case c.splits[node.Type()]:
			flush()
			if w <= c.maxWords {
				blocks = append(blocks, c.namedBlock(node, parentPath))
			} else {
				// Oversized definition: recurse into its children so the split
				// falls on method/statement boundaries, carrying the def name
				// into the enclosing path.
				name := extractSymbolName(node, c.language, c.src)
				sub := c.chunkNodes(collectChildren(node), joinPath(parentPath, name), false)
				if len(sub) == 0 {
					sub = c.windowBlocks(node, parentPath, name, c.refinedType(node), c.maxWords)
				}
				blocks = append(blocks, sub...)
			}

		case w > c.maxWords:
			// Oversized trivia (e.g. a huge top-level statement or data literal):
			// recurse into its children, or window-split if it is a leaf.
			flush()
			grand := collectChildren(node)
			if len(grand) > 1 {
				blocks = append(blocks, c.chunkNodes(grand, parentPath, topLevel)...)
			} else {
				blocks = append(blocks, c.windowBlocks(node, parentPath, triviaName, triviaType, c.maxWords)...)
			}

		default:
			// Small trivia: greedily merge with adjacent siblings up to budget.
			if bufWords+w > c.maxWords {
				flush()
			}
			buf = append(buf, node)
			bufWords += w
		}
	}
	flush()
	return blocks
}

// namedBlock builds a block for a definition node that fits the budget.
func (c *chunkCtx) namedBlock(node *sitter.Node, parentPath string) CodeBlock {
	return CodeBlock{
		Text:       node.Content(c.src),
		Language:   c.language,
		SymbolName: extractSymbolName(node, c.language, c.src),
		SymbolType: c.refinedType(node),
		SymbolPath: parentPath,
		StartLine:  int(node.StartPoint().Row) + 1,
		EndLine:    int(node.EndPoint().Row) + 1,
		FilePath:   c.relPath,
	}
}

// refinedType maps a node type to a symbol type, refining Python decorated
// definitions to the underlying class/function.
func (c *chunkCtx) refinedType(node *sitter.Node) string {
	symbolType := nodeSymbolType(node.Type())
	if symbolType == "decorated" && c.language == "python" {
		for i := 0; i < int(node.ChildCount()); i++ {
			switch node.Child(i).Type() {
			case "class_definition":
				return "class"
			case "function_definition":
				return "function"
			}
		}
	}
	return symbolType
}

// spanBlock builds a single block from a contiguous run of nodes, using the
// exact source span so original formatting is preserved. Returns ok=false if
// the span is blank.
func (c *chunkCtx) spanBlock(nodes []*sitter.Node, parentPath, name, symbolType string) (CodeBlock, bool) {
	first, last := nodes[0], nodes[len(nodes)-1]
	text := string(c.src[first.StartByte():last.EndByte()])
	if strings.TrimSpace(text) == "" {
		return CodeBlock{}, false
	}
	return CodeBlock{
		Text:       text,
		Language:   c.language,
		SymbolName: name,
		SymbolType: symbolType,
		SymbolPath: parentPath,
		StartLine:  int(first.StartPoint().Row) + 1,
		EndLine:    int(last.EndPoint().Row) + 1,
		FilePath:   c.relPath,
	}, true
}

// windowBlocks is the last-resort splitter for an oversized leaf node with no
// further structure. It splits the node's text into overlapping word windows.
func (c *chunkCtx) windowBlocks(node *sitter.Node, parentPath, name, symbolType string, maxWords int) []CodeBlock {
	startLine := int(node.StartPoint().Row) + 1
	windows := splitWords(node.Content(c.src), maxWords, c.overlap)
	blocks := make([]CodeBlock, 0, len(windows))
	for _, w := range windows {
		if strings.TrimSpace(w) == "" {
			continue
		}
		blocks = append(blocks, CodeBlock{
			Text:       w,
			Language:   c.language,
			SymbolName: name,
			SymbolType: symbolType,
			SymbolPath: parentPath,
			StartLine:  startLine,
			EndLine:    int(node.EndPoint().Row) + 1,
			FilePath:   c.relPath,
		})
	}
	return blocks
}

// joinPath appends a symbol name to an enclosing path.
func joinPath(parent, name string) string {
	if parent == "" {
		return name
	}
	return parent + " > " + name
}

// wordCount estimates token count by whitespace splitting.
func wordCount(text string) int {
	return len(strings.Fields(text))
}

// splitWords splits text into overlapping word windows of at most size words.
func splitWords(text string, size, overlap int) []string {
	if size < 1 {
		size = 1
	}
	if overlap < 0 || overlap >= size {
		overlap = 0
	}
	words := strings.Fields(text)
	if len(words) <= size {
		return []string{text}
	}
	var out []string
	for start := 0; start < len(words); {
		end := start + size
		if end > len(words) {
			end = len(words)
		}
		out = append(out, strings.Join(words[start:end], " "))
		if end >= len(words) {
			break
		}
		start = end - overlap
	}
	return out
}

// plainTextDoc splits a non-parsed file into size-bounded module_top blocks.
func plainTextDoc(sourceBytes []byte, language, relativePath string, maxWords, overlap int) *CodeDocument {
	doc := &CodeDocument{FilePath: relativePath, Language: language}
	fullText := string(sourceBytes)
	if strings.TrimSpace(fullText) == "" {
		return doc
	}
	lineCount := strings.Count(fullText, "\n") + 1
	windows := splitWords(fullText, maxWords, overlap)
	for _, w := range windows {
		if strings.TrimSpace(w) == "" {
			continue
		}
		doc.Blocks = append(doc.Blocks, CodeBlock{
			Text:       w,
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
