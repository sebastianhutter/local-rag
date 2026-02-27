"""Tree-sitter based code parser for local-rag.

Parses source code files into structural blocks (functions, classes, etc.)
using tree-sitter grammars via tree-sitter-language-pack.
"""

import logging
from dataclasses import dataclass, field
from pathlib import Path

logger = logging.getLogger(__name__)


@dataclass
class CodeBlock:
    """A structural block of source code."""

    text: str
    language: str
    symbol_name: str
    symbol_type: str  # "function", "class", "method", "block", "module_top"
    start_line: int  # 1-based
    end_line: int  # 1-based
    file_path: str  # relative path within repo


@dataclass
class CodeDocument:
    """A parsed code file containing structural blocks."""

    file_path: str
    language: str
    blocks: list[CodeBlock] = field(default_factory=list)


# Map file extensions to tree-sitter language names
_CODE_EXTENSION_MAP: dict[str, str] = {
    ".py": "python",
    ".go": "go",
    ".tf": "hcl",
    ".tfvars": "hcl",
    ".hcl": "hcl",
    ".ts": "typescript",
    ".tsx": "tsx",
    ".js": "javascript",
    ".jsx": "javascript",
    ".rs": "rust",
    ".java": "java",
    ".c": "c",
    ".h": "c",
    ".cpp": "cpp",
    ".cc": "cpp",
    ".hpp": "cpp",
    ".cs": "csharp",
    ".rb": "ruby",
    ".sh": "bash",
    ".bash": "bash",
    ".yaml": "yaml",
    ".yml": "yaml",
    ".json": "json",
    ".txt": "plaintext",
    ".csv": "plaintext",
    ".md": "markdown",
    ".rst": "plaintext",
    ".toml": "toml",
    ".sql": "sql",
    ".xml": "xml",
    ".html": "html",
    ".htm": "html",
    ".css": "css",
    ".scss": "scss",
}

# Filename-based language detection (no extension match)
_CODE_FILENAME_MAP: dict[str, str] = {
    "Dockerfile": "dockerfile",
    "Makefile": "make",
}

# Node types that trigger splitting per language
_SPLIT_NODE_TYPES: dict[str, set[str]] = {
    "python": {"function_definition", "class_definition", "decorated_definition"},
    "go": {"function_declaration", "method_declaration", "type_declaration"},
    "hcl": {"block"},
    "typescript": {
        "function_declaration",
        "class_declaration",
        "export_statement",
        "interface_declaration",
        "type_alias_declaration",
        "enum_declaration",
    },
    "tsx": {
        "function_declaration",
        "class_declaration",
        "export_statement",
        "interface_declaration",
        "type_alias_declaration",
        "enum_declaration",
    },
    "javascript": {
        "function_declaration",
        "class_declaration",
        "export_statement",
    },
    "rust": {
        "function_item",
        "struct_item",
        "enum_item",
        "impl_item",
        "trait_item",
        "mod_item",
    },
    "java": {"class_declaration", "interface_declaration", "enum_declaration", "method_declaration"},
    "c": {"function_definition", "struct_specifier", "enum_specifier"},
    "cpp": {"function_definition", "class_specifier", "struct_specifier", "enum_specifier"},
    "csharp": {"class_declaration", "interface_declaration", "method_declaration", "enum_declaration"},
    "ruby": {"method", "class", "module"},
    "bash": {"function_definition"},
    "yaml": set(),  # no structural splitting for YAML
    "dockerfile": set(),  # no structural splitting for Dockerfile
    "json": set(),  # no structural splitting for JSON
    "plaintext": set(),  # no tree-sitter parser, handled as raw text
    "markdown": set(),  # no structural splitting for Markdown
    "toml": set(),  # no structural splitting for TOML
    "sql": set(),  # no structural splitting for SQL
    "xml": set(),  # no structural splitting for XML
    "html": set(),  # no structural splitting for HTML
    "css": set(),  # no structural splitting for CSS
    "scss": set(),  # no structural splitting for SCSS
    "make": set(),  # no structural splitting for Makefile
}


def get_supported_extensions() -> set[str]:
    """Return the set of file extensions supported by the code parser."""
    return set(_CODE_EXTENSION_MAP.keys())


def is_code_file(path: Path) -> bool:
    """Check if a file is a supported code file."""
    if path.name in _CODE_FILENAME_MAP:
        return True
    return path.suffix.lower() in _CODE_EXTENSION_MAP


def get_language(path: Path) -> str | None:
    """Get the tree-sitter language name for a file path."""
    if path.name in _CODE_FILENAME_MAP:
        return _CODE_FILENAME_MAP[path.name]
    return _CODE_EXTENSION_MAP.get(path.suffix.lower())


def _extract_symbol_name(node, language: str, source_bytes: bytes) -> str:
    """Extract a human-readable symbol name from a tree-sitter node.

    Args:
        node: Tree-sitter node.
        language: Language name.
        source_bytes: Raw source file bytes.

    Returns:
        Symbol name string.
    """
    if language == "hcl":
        # HCL blocks: e.g. resource "aws_instance" "web" -> "resource aws_instance web"
        parts = []
        for child in node.children:
            if child.type == "identifier":
                parts.append(child.text.decode("utf-8", errors="replace"))
            elif child.type == "string_lit":
                text = child.text.decode("utf-8", errors="replace").strip('"')
                parts.append(text)
            elif child.type == "block":
                break  # stop at nested block body
            elif child.type == "{":
                break
        return " ".join(parts) if parts else node.type

    if language == "python":
        # Handle decorated definitions by looking inside
        target = node
        if node.type == "decorated_definition":
            for child in node.children:
                if child.type in ("function_definition", "class_definition"):
                    target = child
                    break

        for child in target.children:
            if child.type == "identifier":
                return child.text.decode("utf-8", errors="replace")
        return target.type

    if language == "go":
        if node.type == "type_declaration":
            for child in node.children:
                if child.type == "type_spec":
                    for gc in child.children:
                        if gc.type == "type_identifier":
                            return gc.text.decode("utf-8", errors="replace")
        for child in node.children:
            if child.type in ("identifier", "field_identifier"):
                return child.text.decode("utf-8", errors="replace")
        return node.type

    if language in ("typescript", "tsx", "javascript"):
        if node.type == "export_statement":
            # Look inside the exported declaration
            for child in node.children:
                if child.type in (
                    "function_declaration",
                    "class_declaration",
                    "interface_declaration",
                    "type_alias_declaration",
                    "enum_declaration",
                ):
                    return _extract_symbol_name(child, language, source_bytes)
                if child.type == "lexical_declaration":
                    for gc in child.children:
                        if gc.type == "variable_declarator":
                            for ggc in gc.children:
                                if ggc.type == "identifier":
                                    return ggc.text.decode("utf-8", errors="replace")
            return "export"

        for child in node.children:
            if child.type in ("identifier", "type_identifier"):
                return child.text.decode("utf-8", errors="replace")
        return node.type

    if language == "rust":
        for child in node.children:
            if child.type in ("identifier", "type_identifier"):
                return child.text.decode("utf-8", errors="replace")
        return node.type

    if language in ("java", "csharp"):
        for child in node.children:
            if child.type == "identifier":
                return child.text.decode("utf-8", errors="replace")
        return node.type

    if language == "ruby":
        for child in node.children:
            if child.type in ("identifier", "constant"):
                return child.text.decode("utf-8", errors="replace")
        return node.type

    if language == "c" or language == "cpp":
        # function_definition -> declarator -> identifier
        for child in node.children:
            if child.type in ("identifier", "type_identifier"):
                return child.text.decode("utf-8", errors="replace")
            if "declarator" in child.type:
                for gc in child.children:
                    if gc.type in ("identifier", "field_identifier"):
                        return gc.text.decode("utf-8", errors="replace")
                    if "declarator" in gc.type:
                        for ggc in gc.children:
                            if ggc.type in ("identifier", "field_identifier"):
                                return ggc.text.decode("utf-8", errors="replace")
        return node.type

    # Fallback: look for any identifier child
    for child in node.children:
        if child.type == "identifier":
            return child.text.decode("utf-8", errors="replace")
    return node.type


def _node_symbol_type(node_type: str, language: str) -> str:
    """Map a tree-sitter node type to a human-readable symbol type."""
    type_map = {
        "function_definition": "function",
        "function_declaration": "function",
        "function_item": "function",
        "method_declaration": "method",
        "method": "method",
        "class_definition": "class",
        "class_declaration": "class",
        "class_specifier": "class",
        "decorated_definition": "decorated",
        "type_declaration": "type",
        "type_alias_declaration": "type",
        "interface_declaration": "interface",
        "enum_declaration": "enum",
        "enum_specifier": "enum",
        "enum_item": "enum",
        "struct_item": "struct",
        "struct_specifier": "struct",
        "impl_item": "impl",
        "trait_item": "trait",
        "mod_item": "module",
        "module": "module",
        "export_statement": "export",
        "block": "block",  # HCL
    }
    result = type_map.get(node_type, "block")
    return result


def parse_code_file(
    file_path: Path, language: str, relative_path: str
) -> CodeDocument | None:
    """Parse a source code file into structural blocks using tree-sitter.

    Args:
        file_path: Absolute path to the source file.
        language: Tree-sitter language name (e.g. "python", "go", "hcl").
        relative_path: Relative path within the repository (for display).

    Returns:
        CodeDocument with parsed blocks, or None if parsing fails.
    """
    try:
        source_bytes = file_path.read_bytes()
    except OSError as e:
        logger.error("Cannot read file %s: %s", file_path, e)
        return None

    # Plaintext files (txt, csv) have no tree-sitter grammar — treat as single block
    if language == "plaintext":
        full_text = source_bytes.decode("utf-8", errors="replace")
        doc = CodeDocument(file_path=relative_path, language=language)
        if full_text.strip():
            line_count = full_text.count("\n") + 1
            doc.blocks.append(
                CodeBlock(
                    text=full_text,
                    language=language,
                    symbol_name="(file)",
                    symbol_type="module_top",
                    start_line=1,
                    end_line=line_count,
                    file_path=relative_path,
                )
            )
        return doc

    try:
        from tree_sitter_language_pack import get_parser
    except ImportError:
        logger.error("tree-sitter-language-pack is not installed")
        return None

    try:
        parser = get_parser(language)
    except Exception as e:
        logger.error("Unsupported language '%s': %s", language, e)
        return None

    try:
        tree = parser.parse(source_bytes)
    except Exception as e:
        logger.error("Failed to parse %s: %s", file_path, e)
        return None

    root = tree.root_node
    split_types = _SPLIT_NODE_TYPES.get(language, set())

    doc = CodeDocument(file_path=relative_path, language=language)

    # For HCL, the root has a "body" child that contains the actual blocks
    top_level_children = list(root.children)
    if language == "hcl" and len(top_level_children) == 1 and top_level_children[0].type == "body":
        top_level_children = list(top_level_children[0].children)

    # Accumulate non-split nodes into module_top blocks
    top_level_lines: list[str] = []
    top_start_line: int | None = None
    top_end_line: int | None = None

    def _flush_top_level() -> None:
        nonlocal top_level_lines, top_start_line, top_end_line
        if top_level_lines and top_start_line is not None:
            text = "\n".join(top_level_lines)
            if text.strip():
                doc.blocks.append(
                    CodeBlock(
                        text=text,
                        language=language,
                        symbol_name="(top-level)",
                        symbol_type="module_top",
                        start_line=top_start_line,
                        end_line=top_end_line or top_start_line,
                        file_path=relative_path,
                    )
                )
        top_level_lines = []
        top_start_line = None
        top_end_line = None

    for child in top_level_children:
        if child.type in split_types:
            _flush_top_level()

            node_text = child.text.decode("utf-8", errors="replace")
            start_line = child.start_point.row + 1  # convert 0-based to 1-based
            end_line = child.end_point.row + 1

            symbol_name = _extract_symbol_name(child, language, source_bytes)
            symbol_type = _node_symbol_type(child.type, language)

            # For decorated definitions, refine based on the inner node
            if symbol_type == "decorated" and language == "python":
                for inner in child.children:
                    if inner.type == "class_definition":
                        symbol_type = "class"
                        break
                    elif inner.type == "function_definition":
                        symbol_type = "function"
                        break

            doc.blocks.append(
                CodeBlock(
                    text=node_text,
                    language=language,
                    symbol_name=symbol_name,
                    symbol_type=symbol_type,
                    start_line=start_line,
                    end_line=end_line,
                    file_path=relative_path,
                )
            )
        else:
            # Accumulate into top-level block
            node_text = child.text.decode("utf-8", errors="replace")
            start = child.start_point.row + 1
            end = child.end_point.row + 1
            if top_start_line is None:
                top_start_line = start
            top_end_line = end
            top_level_lines.append(node_text)

    _flush_top_level()

    # If no structural blocks found (e.g., YAML, Dockerfile, or files with
    # only top-level code), treat the entire file as one block
    if not doc.blocks:
        full_text = source_bytes.decode("utf-8", errors="replace")
        if full_text.strip():
            line_count = full_text.count("\n") + 1
            doc.blocks.append(
                CodeBlock(
                    text=full_text,
                    language=language,
                    symbol_name="(file)",
                    symbol_type="module_top",
                    start_line=1,
                    end_line=line_count,
                    file_path=relative_path,
                )
            )

    return doc
