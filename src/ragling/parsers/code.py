"""Tree-sitter based code parser for local-rag.

Parses source code files into structural blocks (functions, classes, etc.)
using tree-sitter grammars via tree-sitter-language-pack.
"""

from __future__ import annotations

import logging
from dataclasses import dataclass, field
from pathlib import Path
from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from tree_sitter import Node

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
    ".ps1": "powershell",
    ".psm1": "powershell",
    ".yaml": "yaml",
    ".yml": "yaml",
    ".zig": "zig",
    ".r": "r",
    ".rmd": "r",
    ".lua": "lua",
    ".pl": "perl",
    ".pm": "perl",
    ".dart": "dart",
    ".kt": "kotlin",
}

# Filename-based language detection (no extension match)
_CODE_FILENAME_MAP: dict[str, str] = {
    "Dockerfile": "dockerfile",
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
    "java": {
        "class_declaration",
        "interface_declaration",
        "enum_declaration",
        "method_declaration",
    },
    "c": {"function_definition", "struct_specifier", "enum_specifier"},
    "cpp": {"function_definition", "class_specifier", "struct_specifier", "enum_specifier"},
    "csharp": {
        "class_declaration",
        "interface_declaration",
        "method_declaration",
        "enum_declaration",
    },
    "ruby": {"method", "class", "module"},
    "bash": {"function_definition"},
    "powershell": {"function_statement", "class_statement"},
    "yaml": set(),  # no structural splitting for YAML
    "dockerfile": set(),  # no structural splitting for Dockerfile
    "zig": {"Decl", "TestDecl", "ComptimeDecl"},
    "r": {"binary_operator"},
    "lua": {"function_declaration", "variable_declaration"},
    "perl": {"subroutine_declaration_statement", "package_statement"},
    "dart": {
        "class_definition",
        "enum_declaration",
        "mixin_declaration",
        "extension_declaration",
        "extension_type_declaration",
        "type_alias",
        # function_signature and getter_signature are handled specially in
        # parse_code_file — they are merged with the following function_body
        # sibling before being emitted as a block.
        "function_signature",
        "getter_signature",
    },
    "kotlin": {"class_declaration", "function_declaration", "object_declaration"},
}

# Dart: node types that represent a signature which must be merged with the
# immediately following ``function_body`` sibling to form a complete block.
_DART_SIGNATURE_TYPES: set[str] = {"function_signature", "getter_signature"}


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

    if language == "kotlin":
        # Kotlin: class_declaration, function_declaration, object_declaration
        # Name is in type_identifier (classes/objects) or simple_identifier (functions)
        for child in node.children:
            if child.type == "type_identifier":
                return child.text.decode("utf-8", errors="replace")
            if child.type == "simple_identifier":
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

    if language == "lua":
        if node.type == "function_declaration":
            for child in node.children:
                if child.type == "identifier":
                    return child.text.decode("utf-8", errors="replace")
                if child.type == "dot_index_expression":
                    # e.g. M.method -> "M.method"
                    return child.text.decode("utf-8", errors="replace")
                if child.type == "method_index_expression":
                    # e.g. M:otherMethod -> "M:otherMethod"
                    return child.text.decode("utf-8", errors="replace")
            return node.type
        if node.type == "variable_declaration":
            # local M = {} or local f = function(...)
            for child in node.children:
                if child.type == "assignment_statement":
                    for gc in child.children:
                        if gc.type == "variable_list":
                            for ggc in gc.children:
                                if ggc.type == "identifier":
                                    return ggc.text.decode("utf-8", errors="replace")
            return node.type
        return node.type

    if language == "perl":
        if node.type == "subroutine_declaration_statement":
            # subroutine_declaration_statement -> bareword (sub name)
            for child in node.children:
                if child.type == "bareword":
                    return child.text.decode("utf-8", errors="replace")
            return node.type
        if node.type == "package_statement":
            # package_statement -> package (keyword) -> package (name)
            # The second child of type "package" is the package name
            found_keyword = False
            for child in node.children:
                if child.type == "package":
                    if not found_keyword:
                        found_keyword = True  # skip the "package" keyword
                    else:
                        return child.text.decode("utf-8", errors="replace")
            return node.type
        return node.type

    if language == "dart":
        if node.type == "getter_signature":
            # getter_signature: type_identifier, get, identifier
            # We want the identifier AFTER the "get" keyword, not the return type.
            for child in node.children:
                if child.type == "identifier":
                    return child.text.decode("utf-8", errors="replace")
            return node.type
        # For most Dart nodes (class_definition, enum_declaration, mixin_declaration,
        # extension_declaration, extension_type_declaration, type_alias,
        # function_signature), the first identifier child holds the name.
        for child in node.children:
            if child.type == "identifier":
                return child.text.decode("utf-8", errors="replace")
            if child.type == "type_identifier":
                return child.text.decode("utf-8", errors="replace")
        return node.type

    if language == "zig":
        if node.type == "TestDecl":
            for child in node.children:
                if child.type == "STRINGLITERALSINGLE":
                    return child.text.decode("utf-8", errors="replace").strip('"')
            return "test"
        if node.type == "ComptimeDecl":
            return "(comptime)"
        # Decl: look inside FnProto or VarDecl for IDENTIFIER
        for child in node.children:
            if child.type == "FnProto":
                for gc in child.children:
                    if gc.type == "IDENTIFIER":
                        return gc.text.decode("utf-8", errors="replace")
            if child.type == "VarDecl":
                for gc in child.children:
                    if gc.type == "IDENTIFIER":
                        return gc.text.decode("utf-8", errors="replace")
        return node.type

    if language == "powershell":
        # function_statement: child function_name holds the name
        # class_statement: child simple_name holds the name
        for child in node.children:
            if child.type == "function_name":
                return child.text.decode("utf-8", errors="replace")
            if child.type == "simple_name":
                return child.text.decode("utf-8", errors="replace")
        return node.type

    if language == "r":
        # R: binary_operator nodes for function assignments
        # Structure: identifier <- function_definition  or  identifier = function_definition
        for child in node.children:
            if child.type == "identifier":
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


def _node_symbol_type(node_type: str, language: str, node: Node | None = None) -> str:
    """Map a tree-sitter node type to a human-readable symbol type.

    Args:
        node_type: The tree-sitter node type string (e.g. "function_definition").
        language: Language name (e.g. "python", "zig").
        node: Optional tree-sitter Node, used for languages like Zig where the
            node type alone is insufficient and child nodes must be inspected to
            refine the symbol type (e.g. distinguishing functions from structs
            within a Zig ``Decl`` node).

    Returns:
        Human-readable symbol type string (e.g. "function", "class", "struct").
    """
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
        "Decl": "declaration",  # Zig — refined below for functions/types
        "TestDecl": "test",  # Zig
        "ComptimeDecl": "comptime",  # Zig
        "function_statement": "function",  # PowerShell (functions and filters)
        "class_statement": "class",  # PowerShell
        "variable_declaration": "variable",  # Lua — refined below for function assignments
        "subroutine_declaration_statement": "subroutine",  # Perl
        "package_statement": "package",  # Perl
        "object_declaration": "object",  # Kotlin
        # Dart
        "mixin_declaration": "mixin",
        "extension_declaration": "extension",
        "extension_type_declaration": "extension_type",
        "type_alias": "type",
        "function_signature": "function",
        "getter_signature": "getter",
    }
    result = type_map.get(node_type, "block")

    # Zig: refine Decl based on inner structure
    if language == "zig" and node_type == "Decl":
        if node is not None:
            for child in node.children:
                if child.type == "FnProto":
                    return "function"
                if child.type == "VarDecl":
                    # Peek at the assigned value to distinguish struct/enum/union/error
                    for gc in child.children:
                        if gc.type == "ErrorUnionExpr":
                            val = (gc.text or b"").decode("utf-8", errors="replace")
                            if val.startswith("struct"):
                                return "struct"
                            if val.startswith("enum"):
                                return "enum"
                            if val.startswith("union"):
                                return "union"
                            if val.startswith("error"):
                                return "error_set"
                    return "variable"

    # R: refine binary_operator based on whether it assigns a function
    if language == "r" and node_type == "binary_operator":
        if node is not None:
            for child in node.children:
                if child.type == "function_definition":
                    return "function"
        return "block"

    # Lua: refine function_declaration for method-style (M.method / M:method)
    if language == "lua" and node_type == "function_declaration":
        if node is not None:
            for child in node.children:
                if child.type in ("dot_index_expression", "method_index_expression"):
                    return "method"
        return "function"

    # Lua: refine variable_declaration — distinguish variable from function assignment
    if language == "lua" and node_type == "variable_declaration":
        if node is not None:
            for child in node.children:
                if child.type == "assignment_statement":
                    for gc in child.children:
                        if gc.type == "expression_list":
                            for ggc in gc.children:
                                if ggc.type == "function_definition":
                                    return "function"
        return "variable"

    # Kotlin: refine class_declaration based on keyword children
    if language == "kotlin" and node_type == "class_declaration":
        if node is not None:
            child_types = {c.type for c in node.children}
            if "interface" in child_types:
                return "interface"
            if "enum" in child_types:
                return "enum"
            # Check modifiers for data class
            for child in node.children:
                if child.type == "modifiers":
                    for mod in child.children:
                        if mod.type == "class_modifier":
                            mod_text = (mod.text or b"").decode("utf-8", errors="replace")
                            if mod_text == "data":
                                return "data_class"
            return "class"

    return result


def parse_code_file(file_path: Path, language: str, relative_path: str) -> CodeDocument | None:
    """Parse a source code file into structural blocks using tree-sitter.

    Args:
        file_path: Absolute path to the source file.
        language: Tree-sitter language name (e.g. "python", "go", "hcl").
        relative_path: Relative path within the repository (for display).

    Returns:
        CodeDocument with parsed blocks, or None if parsing fails.
    """
    try:
        from tree_sitter_language_pack import get_parser
    except ImportError:
        logger.error("tree-sitter-language-pack is not installed")
        return None

    try:
        parser = get_parser(language)  # type: ignore[arg-type]
    except Exception as e:
        logger.error("Unsupported language '%s': %s", language, e)
        return None

    try:
        source_bytes = file_path.read_bytes()
    except OSError as e:
        logger.error("Cannot read file %s: %s", file_path, e)
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

    # PowerShell: root is program -> statement_list -> actual statements
    if (
        language == "powershell"
        and len(top_level_children) == 1
        and top_level_children[0].type == "statement_list"
    ):
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

    # Zig: track pending `pub` visibility modifier to prepend to next Decl
    zig_pending_pub_line: int | None = None

    # Dart: track pending signature node (function_signature / getter_signature)
    # to merge with the following function_body sibling.
    dart_pending_signature: dict | None = None

    for i, child in enumerate(top_level_children):
        # Zig: `pub` is a separate sibling node before Decl — capture it
        if language == "zig" and child.type == "pub":
            _flush_top_level()
            zig_pending_pub_line = child.start_point.row + 1
            continue

        # Dart: merge function_signature/getter_signature with following function_body
        if language == "dart" and child.type in _DART_SIGNATURE_TYPES:
            _flush_top_level()
            dart_pending_signature = {
                "node": child,
                "text": (child.text or b"").decode("utf-8", errors="replace"),
                "start_line": child.start_point.row + 1,
                "end_line": child.end_point.row + 1,
            }
            continue

        if language == "dart" and child.type == "function_body" and dart_pending_signature:
            # Merge body with the pending signature
            sig = dart_pending_signature
            body_text = (child.text or b"").decode("utf-8", errors="replace")
            merged_text = sig["text"] + " " + body_text
            end_line = child.end_point.row + 1
            sig_node = sig["node"]
            symbol_name = _extract_symbol_name(sig_node, language, source_bytes)
            symbol_type = _node_symbol_type(sig_node.type, language, sig_node)

            doc.blocks.append(
                CodeBlock(
                    text=merged_text,
                    language=language,
                    symbol_name=symbol_name,
                    symbol_type=symbol_type,
                    start_line=sig["start_line"],
                    end_line=end_line,
                    file_path=relative_path,
                )
            )
            dart_pending_signature = None
            continue

        # If we had a pending Dart signature but the next node isn't function_body,
        # emit the signature alone (shouldn't normally happen, but handle gracefully)
        if dart_pending_signature is not None:
            sig = dart_pending_signature
            sig_node = sig["node"]
            symbol_name = _extract_symbol_name(sig_node, language, source_bytes)
            symbol_type = _node_symbol_type(sig_node.type, language, sig_node)
            doc.blocks.append(
                CodeBlock(
                    text=sig["text"],
                    language=language,
                    symbol_name=symbol_name,
                    symbol_type=symbol_type,
                    start_line=sig["start_line"],
                    end_line=sig["end_line"],
                    file_path=relative_path,
                )
            )
            dart_pending_signature = None

        if child.type in split_types:
            # R: only split binary_operator when it assigns a function_definition;
            # non-function assignments (e.g. threshold <- 0.05) stay as top-level
            if language == "r" and child.type == "binary_operator":
                has_func = any(c.type == "function_definition" for c in child.children)
                if not has_func:
                    node_text = (child.text or b"").decode("utf-8", errors="replace")
                    start = child.start_point.row + 1
                    end = child.end_point.row + 1
                    if top_start_line is None:
                        top_start_line = start
                    top_end_line = end
                    top_level_lines.append(node_text)
                    continue

            _flush_top_level()

            node_text = (child.text or b"").decode("utf-8", errors="replace")
            start_line = child.start_point.row + 1  # convert 0-based to 1-based
            end_line = child.end_point.row + 1

            # Zig: prepend `pub` if it preceded this declaration
            if zig_pending_pub_line is not None:
                node_text = "pub " + node_text
                start_line = zig_pending_pub_line
                zig_pending_pub_line = None

            symbol_name = _extract_symbol_name(child, language, source_bytes)
            symbol_type = _node_symbol_type(child.type, language, child)

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
            zig_pending_pub_line = None  # clear stale pub if any
            # Accumulate into top-level block
            node_text = (child.text or b"").decode("utf-8", errors="replace")
            start = child.start_point.row + 1
            end = child.end_point.row + 1
            if top_start_line is None:
                top_start_line = start
            top_end_line = end
            top_level_lines.append(node_text)

    # Flush any remaining pending Dart signature
    if dart_pending_signature is not None:
        sig = dart_pending_signature
        sig_node = sig["node"]
        symbol_name = _extract_symbol_name(sig_node, language, source_bytes)
        symbol_type = _node_symbol_type(sig_node.type, language, sig_node)
        doc.blocks.append(
            CodeBlock(
                text=sig["text"],
                language=language,
                symbol_name=symbol_name,
                symbol_type=symbol_type,
                start_line=sig["start_line"],
                end_line=sig["end_line"],
                file_path=relative_path,
            )
        )
        dart_pending_signature = None

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
