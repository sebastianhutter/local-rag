"""Tests for ragling.parsers.code -- extension map, language detection, and parsing."""

from pathlib import Path

from ragling.parsers.code import get_language, is_code_file, parse_code_file


class TestCodeExtensionMap:
    """Tests for _CODE_EXTENSION_MAP and file selection via is_code_file."""

    def test_python_is_code_file(self) -> None:
        assert is_code_file(Path("main.py")) is True

    def test_javascript_is_code_file(self) -> None:
        assert is_code_file(Path("app.js")) is True

    def test_typescript_is_code_file(self) -> None:
        assert is_code_file(Path("index.ts")) is True

    def test_tsx_is_code_file(self) -> None:
        assert is_code_file(Path("component.tsx")) is True

    def test_go_is_code_file(self) -> None:
        assert is_code_file(Path("server.go")) is True

    def test_rust_is_code_file(self) -> None:
        assert is_code_file(Path("lib.rs")) is True

    def test_java_is_code_file(self) -> None:
        assert is_code_file(Path("Main.java")) is True

    def test_c_is_code_file(self) -> None:
        assert is_code_file(Path("main.c")) is True

    def test_cpp_is_code_file(self) -> None:
        assert is_code_file(Path("main.cpp")) is True

    def test_header_is_code_file(self) -> None:
        assert is_code_file(Path("header.h")) is True

    def test_ruby_is_code_file(self) -> None:
        assert is_code_file(Path("script.rb")) is True

    def test_bash_is_code_file(self) -> None:
        assert is_code_file(Path("deploy.sh")) is True

    def test_terraform_is_code_file(self) -> None:
        assert is_code_file(Path("main.tf")) is True

    def test_hcl_is_code_file(self) -> None:
        assert is_code_file(Path("config.hcl")) is True

    def test_yaml_is_code_file(self) -> None:
        assert is_code_file(Path("config.yaml")) is True

    def test_yml_is_code_file(self) -> None:
        assert is_code_file(Path("config.yml")) is True

    def test_csharp_is_code_file(self) -> None:
        assert is_code_file(Path("Program.cs")) is True

    def test_zig_is_code_file(self) -> None:
        assert is_code_file(Path("main.zig")) is True

    def test_perl_pl_is_code_file(self) -> None:
        assert is_code_file(Path("script.pl")) is True

    def test_perl_pm_is_code_file(self) -> None:
        assert is_code_file(Path("Module.pm")) is True

    def test_dockerfile_is_code_file(self) -> None:
        """Dockerfile is detected via _CODE_FILENAME_MAP, not extension."""
        assert is_code_file(Path("Dockerfile")) is True

    def test_markdown_is_not_code_file(self) -> None:
        assert is_code_file(Path("readme.md")) is False

    def test_pdf_is_not_code_file(self) -> None:
        assert is_code_file(Path("document.pdf")) is False

    def test_txt_is_not_code_file(self) -> None:
        assert is_code_file(Path("notes.txt")) is False

    def test_docx_is_not_code_file(self) -> None:
        assert is_code_file(Path("report.docx")) is False

    def test_unknown_is_not_code_file(self) -> None:
        assert is_code_file(Path("data.xyz")) is False

    def test_no_extension_is_not_code_file(self) -> None:
        """A file with no extension and no filename match is not a code file."""
        assert is_code_file(Path("Makefile")) is False

    def test_case_insensitive_extension(self) -> None:
        """Extension matching should be case-insensitive."""
        assert is_code_file(Path("main.PY")) is True
        assert is_code_file(Path("app.JS")) is True


class TestGetLanguage:
    """Tests for get_language returning correct language names."""

    def test_python_returns_python(self) -> None:
        assert get_language(Path("main.py")) == "python"

    def test_go_returns_go(self) -> None:
        assert get_language(Path("server.go")) == "go"

    def test_typescript_returns_typescript(self) -> None:
        assert get_language(Path("index.ts")) == "typescript"

    def test_tsx_returns_tsx(self) -> None:
        assert get_language(Path("component.tsx")) == "tsx"

    def test_javascript_returns_javascript(self) -> None:
        assert get_language(Path("app.js")) == "javascript"

    def test_jsx_returns_javascript(self) -> None:
        assert get_language(Path("component.jsx")) == "javascript"

    def test_rust_returns_rust(self) -> None:
        assert get_language(Path("lib.rs")) == "rust"

    def test_java_returns_java(self) -> None:
        assert get_language(Path("Main.java")) == "java"

    def test_c_returns_c(self) -> None:
        assert get_language(Path("main.c")) == "c"

    def test_header_returns_c(self) -> None:
        assert get_language(Path("header.h")) == "c"

    def test_cpp_returns_cpp(self) -> None:
        assert get_language(Path("main.cpp")) == "cpp"

    def test_cc_returns_cpp(self) -> None:
        assert get_language(Path("main.cc")) == "cpp"

    def test_csharp_returns_csharp(self) -> None:
        assert get_language(Path("Program.cs")) == "csharp"

    def test_ruby_returns_ruby(self) -> None:
        assert get_language(Path("script.rb")) == "ruby"

    def test_bash_returns_bash(self) -> None:
        assert get_language(Path("deploy.sh")) == "bash"

    def test_bash_extension_returns_bash(self) -> None:
        assert get_language(Path("script.bash")) == "bash"

    def test_terraform_returns_hcl(self) -> None:
        assert get_language(Path("main.tf")) == "hcl"

    def test_tfvars_returns_hcl(self) -> None:
        assert get_language(Path("vars.tfvars")) == "hcl"

    def test_hcl_returns_hcl(self) -> None:
        assert get_language(Path("config.hcl")) == "hcl"

    def test_yaml_returns_yaml(self) -> None:
        assert get_language(Path("config.yaml")) == "yaml"

    def test_yml_returns_yaml(self) -> None:
        assert get_language(Path("config.yml")) == "yaml"

    def test_zig_returns_zig(self) -> None:
        assert get_language(Path("main.zig")) == "zig"

    def test_perl_pl_returns_perl(self) -> None:
        assert get_language(Path("script.pl")) == "perl"

    def test_perl_pm_returns_perl(self) -> None:
        assert get_language(Path("Module.pm")) == "perl"

    def test_dockerfile_returns_dockerfile(self) -> None:
        """Dockerfile is detected via _CODE_FILENAME_MAP."""
        assert get_language(Path("Dockerfile")) == "dockerfile"

    def test_unsupported_returns_none(self) -> None:
        assert get_language(Path("readme.md")) is None

    def test_unknown_extension_returns_none(self) -> None:
        assert get_language(Path("data.xyz")) is None

    def test_no_extension_non_filename_returns_none(self) -> None:
        """A file with no extension and no filename match returns None."""
        assert get_language(Path("Makefile")) is None

    def test_case_insensitive_extension(self) -> None:
        """Extension matching should be case-insensitive."""
        assert get_language(Path("main.PY")) == "python"
        assert get_language(Path("app.JS")) == "javascript"


class TestZigParsing:
    """Tests for Zig code parsing via parse_code_file."""

    # A comprehensive Zig source file covering all major declaration types
    ZIG_SOURCE = """\
const std = @import("std");

pub fn add(a: i32, b: i32) i32 {
    return a + b;
}

fn privateHelper() void {}

const Point = struct {
    x: f64,
    y: f64,
};

const Color = enum {
    red,
    green,
    blue,
};

test "addition works" {
    const result = add(2, 3);
    try std.testing.expectEqual(@as(i32, 5), result);
}

comptime {
    _ = @import("other.zig");
}
"""

    def _parse_zig(self, tmp_path: Path, source: str | None = None) -> list:
        """Write Zig source to a temp file and parse it, returning blocks."""
        zig_file = tmp_path / "test.zig"
        zig_file.write_text(source if source is not None else self.ZIG_SOURCE)
        doc = parse_code_file(zig_file, "zig", "test.zig")
        assert doc is not None, "parse_code_file returned None"
        return doc.blocks

    def test_parses_without_error(self, tmp_path: Path) -> None:
        """Zig source parses successfully and returns a CodeDocument."""
        zig_file = tmp_path / "test.zig"
        zig_file.write_text(self.ZIG_SOURCE)
        doc = parse_code_file(zig_file, "zig", "test.zig")
        assert doc is not None
        assert doc.language == "zig"
        assert doc.file_path == "test.zig"

    def test_block_count(self, tmp_path: Path) -> None:
        """Zig source produces the expected number of structural blocks.

        Expected blocks:
        1. const std = @import("std"); (variable — Decl with VarDecl)
        2. pub fn add (function)
        3. fn privateHelper (function)
        4. const Point = struct { ... } (struct)
        5. const Color = enum { ... } (enum)
        6. test "addition works" (test)
        7. comptime { ... } (comptime)
        """
        blocks = self._parse_zig(tmp_path)
        assert len(blocks) == 7

    def test_import_declaration(self, tmp_path: Path) -> None:
        """A top-level @import is a Decl/VarDecl classified as 'variable'."""
        blocks = self._parse_zig(tmp_path)
        import_block = blocks[0]
        assert import_block.symbol_type == "variable"
        assert import_block.symbol_name == "std"
        assert "@import" in import_block.text

    def test_pub_function_symbol_name(self, tmp_path: Path) -> None:
        """A pub fn declaration extracts the correct symbol name."""
        blocks = self._parse_zig(tmp_path)
        pub_fn = blocks[1]
        assert pub_fn.symbol_name == "add"

    def test_pub_function_symbol_type(self, tmp_path: Path) -> None:
        """A pub fn declaration is classified as symbol_type 'function'."""
        blocks = self._parse_zig(tmp_path)
        pub_fn = blocks[1]
        assert pub_fn.symbol_type == "function"

    def test_pub_prefix_prepended_to_text(self, tmp_path: Path) -> None:
        """The ``pub`` visibility modifier is prepended to the block text."""
        blocks = self._parse_zig(tmp_path)
        pub_fn = blocks[1]
        assert pub_fn.text.startswith("pub ")

    def test_pub_prefix_adjusts_start_line(self, tmp_path: Path) -> None:
        """The start_line for a pub declaration includes the pub keyword line."""
        blocks = self._parse_zig(tmp_path)
        pub_fn = blocks[1]
        # "pub fn add" starts on line 3 (1-based)
        assert pub_fn.start_line == 3

    def test_private_function(self, tmp_path: Path) -> None:
        """A private fn declaration is correctly parsed."""
        blocks = self._parse_zig(tmp_path)
        priv_fn = blocks[2]
        assert priv_fn.symbol_name == "privateHelper"
        assert priv_fn.symbol_type == "function"
        assert not priv_fn.text.startswith("pub ")

    def test_struct_declaration(self, tmp_path: Path) -> None:
        """A const = struct { ... } declaration is classified as 'struct'."""
        blocks = self._parse_zig(tmp_path)
        struct_block = blocks[3]
        assert struct_block.symbol_name == "Point"
        assert struct_block.symbol_type == "struct"

    def test_enum_declaration(self, tmp_path: Path) -> None:
        """A const = enum { ... } declaration is classified as 'enum'."""
        blocks = self._parse_zig(tmp_path)
        enum_block = blocks[4]
        assert enum_block.symbol_name == "Color"
        assert enum_block.symbol_type == "enum"

    def test_test_declaration_name(self, tmp_path: Path) -> None:
        """A test declaration extracts the test name string."""
        blocks = self._parse_zig(tmp_path)
        test_block = blocks[5]
        assert test_block.symbol_name == "addition works"

    def test_test_declaration_type(self, tmp_path: Path) -> None:
        """A test declaration is classified as symbol_type 'test'."""
        blocks = self._parse_zig(tmp_path)
        test_block = blocks[5]
        assert test_block.symbol_type == "test"

    def test_comptime_declaration(self, tmp_path: Path) -> None:
        """A comptime block is classified as symbol_type 'comptime'."""
        blocks = self._parse_zig(tmp_path)
        comptime_block = blocks[6]
        assert comptime_block.symbol_name == "(comptime)"
        assert comptime_block.symbol_type == "comptime"

    def test_start_end_lines_1_based(self, tmp_path: Path) -> None:
        """start_line and end_line use 1-based line numbers."""
        blocks = self._parse_zig(tmp_path)
        # All blocks should have positive line numbers
        for block in blocks:
            assert block.start_line >= 1
            assert block.end_line >= block.start_line

    def test_file_path_propagated(self, tmp_path: Path) -> None:
        """The relative file_path is propagated to all blocks."""
        blocks = self._parse_zig(tmp_path)
        for block in blocks:
            assert block.file_path == "test.zig"

    def test_language_set_on_blocks(self, tmp_path: Path) -> None:
        """All blocks have language set to 'zig'."""
        blocks = self._parse_zig(tmp_path)
        for block in blocks:
            assert block.language == "zig"

    def test_variable_declaration(self, tmp_path: Path) -> None:
        """A plain const variable (not struct/enum) is classified as 'variable'."""
        source = """\
const max_size: usize = 1024;
"""
        blocks = self._parse_zig(tmp_path, source)
        assert len(blocks) >= 1
        var_block = blocks[0]
        assert var_block.symbol_name == "max_size"
        assert var_block.symbol_type == "variable"

    def test_empty_file_produces_no_blocks(self, tmp_path: Path) -> None:
        """An empty .zig file produces no blocks."""
        zig_file = tmp_path / "empty.zig"
        zig_file.write_text("")
        doc = parse_code_file(zig_file, "zig", "empty.zig")
        assert doc is not None
        assert len(doc.blocks) == 0

    def test_pub_not_carried_across_non_decl_nodes(self, tmp_path: Path) -> None:
        """A stale ``pub`` modifier is cleared if a non-Decl node follows."""
        source = """\
pub fn first() void {}
fn second() void {}
"""
        blocks = self._parse_zig(tmp_path, source)
        # first should have pub prefix, second should not
        first = [b for b in blocks if b.symbol_name == "first"][0]
        second = [b for b in blocks if b.symbol_name == "second"][0]
        assert first.text.startswith("pub ")
        assert not second.text.startswith("pub ")


class TestPowerShellExtensionAndLanguage:
    """Tests for PowerShell extension mapping and language detection."""

    def test_ps1_is_code_file(self) -> None:
        assert is_code_file(Path("script.ps1")) is True

    def test_psm1_is_code_file(self) -> None:
        assert is_code_file(Path("module.psm1")) is True

    def test_ps1_returns_powershell(self) -> None:
        assert get_language(Path("script.ps1")) == "powershell"

    def test_psm1_returns_powershell(self) -> None:
        assert get_language(Path("module.psm1")) == "powershell"


class TestPowerShellParsing:
    """Tests for PowerShell code parsing via parse_code_file."""

    PS_SOURCE = """\
function Get-Greeting {
    param(
        [string]$Name
    )
    return "Hello, $Name"
}

function Set-Config {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory=$true)]
        [string]$Path,
        [hashtable]$Settings
    )
    $Settings | ConvertTo-Json | Set-Content $Path
}

filter Where-Even {
    if ($_ % 2 -eq 0) { $_ }
}

class MyClass {
    [string]$Name
    MyClass([string]$name) {
        $this.Name = $name
    }
    [string] Greet() {
        return "Hello, $($this.Name)"
    }
}
"""

    def _parse_ps(self, tmp_path: Path, source: str | None = None) -> list:
        """Write PowerShell source to a temp file and parse it, returning blocks."""
        ps_file = tmp_path / "test.ps1"
        ps_file.write_text(source if source is not None else self.PS_SOURCE)
        doc = parse_code_file(ps_file, "powershell", "test.ps1")
        assert doc is not None, "parse_code_file returned None"
        return doc.blocks

    def test_parses_without_error(self, tmp_path: Path) -> None:
        """PowerShell source parses successfully and returns a CodeDocument."""
        ps_file = tmp_path / "test.ps1"
        ps_file.write_text(self.PS_SOURCE)
        doc = parse_code_file(ps_file, "powershell", "test.ps1")
        assert doc is not None
        assert doc.language == "powershell"
        assert doc.file_path == "test.ps1"

    def test_block_count(self, tmp_path: Path) -> None:
        """PowerShell source produces the expected number of structural blocks.

        Expected blocks:
        1. function Get-Greeting
        2. function Set-Config
        3. filter Where-Even (also a function_statement)
        4. class MyClass
        """
        blocks = self._parse_ps(tmp_path)
        assert len(blocks) == 4

    def test_function_symbol_name(self, tmp_path: Path) -> None:
        """A function declaration extracts the correct symbol name."""
        blocks = self._parse_ps(tmp_path)
        fn_block = blocks[0]
        assert fn_block.symbol_name == "Get-Greeting"

    def test_function_symbol_type(self, tmp_path: Path) -> None:
        """A function declaration is classified as symbol_type 'function'."""
        blocks = self._parse_ps(tmp_path)
        fn_block = blocks[0]
        assert fn_block.symbol_type == "function"

    def test_function_with_cmdletbinding(self, tmp_path: Path) -> None:
        """A function with [CmdletBinding()] parses correctly."""
        blocks = self._parse_ps(tmp_path)
        fn_block = blocks[1]
        assert fn_block.symbol_name == "Set-Config"
        assert fn_block.symbol_type == "function"

    def test_filter_symbol_name(self, tmp_path: Path) -> None:
        """A filter declaration extracts the correct symbol name."""
        blocks = self._parse_ps(tmp_path)
        filter_block = blocks[2]
        assert filter_block.symbol_name == "Where-Even"

    def test_filter_symbol_type(self, tmp_path: Path) -> None:
        """A filter is classified as symbol_type 'function' (same as function_statement)."""
        blocks = self._parse_ps(tmp_path)
        filter_block = blocks[2]
        assert filter_block.symbol_type == "function"

    def test_class_symbol_name(self, tmp_path: Path) -> None:
        """A class declaration extracts the correct symbol name."""
        blocks = self._parse_ps(tmp_path)
        class_block = blocks[3]
        assert class_block.symbol_name == "MyClass"

    def test_class_symbol_type(self, tmp_path: Path) -> None:
        """A class declaration is classified as symbol_type 'class'."""
        blocks = self._parse_ps(tmp_path)
        class_block = blocks[3]
        assert class_block.symbol_type == "class"

    def test_start_end_lines_1_based(self, tmp_path: Path) -> None:
        """start_line and end_line use 1-based line numbers."""
        blocks = self._parse_ps(tmp_path)
        for block in blocks:
            assert block.start_line >= 1
            assert block.end_line >= block.start_line

    def test_file_path_propagated(self, tmp_path: Path) -> None:
        """The relative file_path is propagated to all blocks."""
        blocks = self._parse_ps(tmp_path)
        for block in blocks:
            assert block.file_path == "test.ps1"

    def test_language_set_on_blocks(self, tmp_path: Path) -> None:
        """All blocks have language set to 'powershell'."""
        blocks = self._parse_ps(tmp_path)
        for block in blocks:
            assert block.language == "powershell"

    def test_function_text_contains_body(self, tmp_path: Path) -> None:
        """The function block text includes the full function body."""
        blocks = self._parse_ps(tmp_path)
        fn_block = blocks[0]
        assert "Get-Greeting" in fn_block.text
        assert "param" in fn_block.text
        assert "return" in fn_block.text

    def test_class_text_contains_methods(self, tmp_path: Path) -> None:
        """The class block text includes method definitions."""
        blocks = self._parse_ps(tmp_path)
        class_block = blocks[3]
        assert "MyClass" in class_block.text
        assert "Greet" in class_block.text

    def test_top_level_code_becomes_module_top(self, tmp_path: Path) -> None:
        """Top-level code outside functions/classes becomes module_top."""
        source = """\
$greeting = "Hello"
Write-Host $greeting

function Get-Name {
    return "World"
}
"""
        blocks = self._parse_ps(tmp_path, source)
        top_blocks = [b for b in blocks if b.symbol_type == "module_top"]
        fn_blocks = [b for b in blocks if b.symbol_type == "function"]
        assert len(top_blocks) == 1
        assert len(fn_blocks) == 1
        assert "$greeting" in top_blocks[0].text

    def test_empty_file_produces_no_blocks(self, tmp_path: Path) -> None:
        """An empty .ps1 file produces no blocks."""
        ps_file = tmp_path / "empty.ps1"
        ps_file.write_text("")
        doc = parse_code_file(ps_file, "powershell", "empty.ps1")
        assert doc is not None
        assert len(doc.blocks) == 0

    def test_only_functions_no_class(self, tmp_path: Path) -> None:
        """A file with only functions and no class parses correctly."""
        source = """\
function First {
    "first"
}

function Second {
    "second"
}
"""
        blocks = self._parse_ps(tmp_path, source)
        assert len(blocks) == 2
        assert blocks[0].symbol_name == "First"
        assert blocks[1].symbol_name == "Second"


class TestDartExtensionAndLanguage:
    """Tests for Dart extension mapping and language detection."""

    def test_dart_is_code_file(self) -> None:
        assert is_code_file(Path("main.dart")) is True

    def test_dart_returns_dart(self) -> None:
        assert get_language(Path("main.dart")) == "dart"

    def test_dart_case_insensitive(self) -> None:
        assert is_code_file(Path("main.DART")) is True
        assert get_language(Path("main.DART")) == "dart"


class TestDartParsing:
    """Tests for Dart code parsing via parse_code_file."""

    # A comprehensive Dart source file covering all major declaration types
    DART_SOURCE = """\
import 'dart:math';

class Animal {
  String name;
  Animal(this.name);
  void speak() {
    print('...');
  }
}

abstract class Shape {
  double area();
}

enum Color { red, green, blue }

mixin Flyable {
  void fly() => print('flying');
}

void topLevelFunction() {
  print('hello');
}

extension StringExt on String {
  bool get isBlank => trim().isEmpty;
}

typedef IntList = List<int>;

sealed class Result {}

class Success extends Result {}
"""

    def _parse_dart(self, tmp_path: Path, source: str | None = None) -> list:
        """Write Dart source to a temp file and parse it, returning blocks."""
        dart_file = tmp_path / "test.dart"
        dart_file.write_text(source if source is not None else self.DART_SOURCE)
        doc = parse_code_file(dart_file, "dart", "test.dart")
        assert doc is not None, "parse_code_file returned None"
        return doc.blocks

    def test_parses_without_error(self, tmp_path: Path) -> None:
        """Dart source parses successfully and returns a CodeDocument."""
        dart_file = tmp_path / "test.dart"
        dart_file.write_text(self.DART_SOURCE)
        doc = parse_code_file(dart_file, "dart", "test.dart")
        assert doc is not None
        assert doc.language == "dart"
        assert doc.file_path == "test.dart"

    def test_block_count(self, tmp_path: Path) -> None:
        """Dart source produces the expected number of structural blocks.

        Expected blocks:
        1. import 'dart:math'; (top-level)
        2. class Animal { ... }
        3. abstract class Shape { ... }
        4. enum Color { ... }
        5. mixin Flyable { ... }
        6. void topLevelFunction() { ... }
        7. extension StringExt on String { ... }
        8. typedef IntList = List<int>;
        9. sealed class Result {}
        10. class Success extends Result {}
        """
        blocks = self._parse_dart(tmp_path)
        assert len(blocks) == 10

    def test_class_declaration(self, tmp_path: Path) -> None:
        """A class declaration extracts the correct name and type."""
        blocks = self._parse_dart(tmp_path)
        animal = [b for b in blocks if b.symbol_name == "Animal"][0]
        assert animal.symbol_type == "class"
        assert "class Animal" in animal.text

    def test_abstract_class_declaration(self, tmp_path: Path) -> None:
        """An abstract class declaration is classified as 'class'."""
        blocks = self._parse_dart(tmp_path)
        shape = [b for b in blocks if b.symbol_name == "Shape"][0]
        assert shape.symbol_type == "class"
        assert "abstract" in shape.text

    def test_enum_declaration(self, tmp_path: Path) -> None:
        """An enum declaration extracts the correct name and type."""
        blocks = self._parse_dart(tmp_path)
        color = [b for b in blocks if b.symbol_name == "Color"][0]
        assert color.symbol_type == "enum"
        assert "red" in color.text

    def test_mixin_declaration(self, tmp_path: Path) -> None:
        """A mixin declaration extracts the correct name and type."""
        blocks = self._parse_dart(tmp_path)
        flyable = [b for b in blocks if b.symbol_name == "Flyable"][0]
        assert flyable.symbol_type == "mixin"
        assert "mixin Flyable" in flyable.text

    def test_top_level_function(self, tmp_path: Path) -> None:
        """A top-level function extracts the correct name and type.

        In Dart, top-level functions are split into function_signature +
        function_body sibling nodes, so the parser must merge them.
        """
        blocks = self._parse_dart(tmp_path)
        func = [b for b in blocks if b.symbol_name == "topLevelFunction"][0]
        assert func.symbol_type == "function"
        assert "void topLevelFunction()" in func.text
        assert "print" in func.text  # body is included

    def test_extension_declaration(self, tmp_path: Path) -> None:
        """An extension declaration extracts the correct name and type."""
        blocks = self._parse_dart(tmp_path)
        ext = [b for b in blocks if b.symbol_name == "StringExt"][0]
        assert ext.symbol_type == "extension"
        assert "extension StringExt" in ext.text

    def test_type_alias(self, tmp_path: Path) -> None:
        """A typedef (type_alias) extracts the correct name and type."""
        blocks = self._parse_dart(tmp_path)
        alias = [b for b in blocks if b.symbol_name == "IntList"][0]
        assert alias.symbol_type == "type"
        assert "typedef" in alias.text

    def test_sealed_class(self, tmp_path: Path) -> None:
        """A sealed class is classified as 'class'."""
        blocks = self._parse_dart(tmp_path)
        result = [b for b in blocks if b.symbol_name == "Result"][0]
        assert result.symbol_type == "class"
        assert "sealed" in result.text

    def test_subclass(self, tmp_path: Path) -> None:
        """A subclass extending another class is classified as 'class'."""
        blocks = self._parse_dart(tmp_path)
        success = [b for b in blocks if b.symbol_name == "Success"][0]
        assert success.symbol_type == "class"
        assert "extends" in success.text

    def test_function_body_merged_with_signature(self, tmp_path: Path) -> None:
        """function_signature and function_body are merged into a single block."""
        source = """\
void greet(String name) {
  print('Hello, $name');
}
"""
        blocks = self._parse_dart(tmp_path, source)
        assert len(blocks) == 1
        func = blocks[0]
        assert func.symbol_name == "greet"
        assert func.symbol_type == "function"
        assert "void greet(String name)" in func.text
        assert "print" in func.text

    def test_extension_type_declaration(self, tmp_path: Path) -> None:
        """An extension type declaration extracts the correct name and type."""
        source = """\
extension type Wrapper(int value) {
  int get doubled => value * 2;
}
"""
        blocks = self._parse_dart(tmp_path, source)
        wrapper = [b for b in blocks if b.symbol_name == "Wrapper"][0]
        assert wrapper.symbol_type == "extension_type"

    def test_start_end_lines_1_based(self, tmp_path: Path) -> None:
        """start_line and end_line use 1-based line numbers."""
        blocks = self._parse_dart(tmp_path)
        for block in blocks:
            assert block.start_line >= 1
            assert block.end_line >= block.start_line

    def test_file_path_propagated(self, tmp_path: Path) -> None:
        """The relative file_path is propagated to all blocks."""
        blocks = self._parse_dart(tmp_path)
        for block in blocks:
            assert block.file_path == "test.dart"

    def test_language_set_on_blocks(self, tmp_path: Path) -> None:
        """All blocks have language set to 'dart'."""
        blocks = self._parse_dart(tmp_path)
        for block in blocks:
            assert block.language == "dart"

    def test_empty_file_produces_no_blocks(self, tmp_path: Path) -> None:
        """An empty .dart file produces no blocks."""
        dart_file = tmp_path / "empty.dart"
        dart_file.write_text("")
        doc = parse_code_file(dart_file, "dart", "empty.dart")
        assert doc is not None
        assert len(doc.blocks) == 0

    def test_top_level_getter(self, tmp_path: Path) -> None:
        """A top-level getter is parsed as a getter with correct name.

        Like functions, getters have signature + body as siblings.
        """
        source = """\
int get topGetter => 42;
"""
        blocks = self._parse_dart(tmp_path, source)
        assert len(blocks) == 1
        getter = blocks[0]
        assert getter.symbol_name == "topGetter"
        assert getter.symbol_type == "getter"
        assert "42" in getter.text

    def test_import_in_top_level(self, tmp_path: Path) -> None:
        """Import statements are captured as module_top blocks."""
        blocks = self._parse_dart(tmp_path)
        top_blocks = [b for b in blocks if b.symbol_type == "module_top"]
        assert len(top_blocks) >= 1
        assert any("import" in b.text for b in top_blocks)


class TestRParsing:
    """Tests for R code parsing via parse_code_file."""

    # A comprehensive R source file covering major declaration types
    R_SOURCE = """\
# Simple function with <- assignment
add <- function(x, y) {
  x + y
}

# S3 method with dotted name
print.myclass <- function(x, ...) {
  cat("MyClass:", x$name, "\\n")
}

# Function with = assignment
my_func = function(data) {
  mean(data)
}

# Non-function assignment (global variable)
threshold <- 0.05

# Library call (top-level)
library(ggplot2)

# setRefClass call (class-like, not a function_definition)
MyClass <- setRefClass("MyClass",
  fields = list(
    name = "character",
    value = "numeric"
  ),
  methods = list(
    greet = function() {
      cat("Hello,", name, "\\n")
    }
  )
)
"""

    def _parse_r(self, tmp_path: Path, source: str | None = None) -> list:
        """Write R source to a temp file and parse it, returning blocks."""
        r_file = tmp_path / "test.R"
        r_file.write_text(source if source is not None else self.R_SOURCE)
        doc = parse_code_file(r_file, "r", "test.R")
        assert doc is not None, "parse_code_file returned None"
        return doc.blocks

    def test_parses_without_error(self, tmp_path: Path) -> None:
        """R source parses successfully and returns a CodeDocument."""
        r_file = tmp_path / "test.R"
        r_file.write_text(self.R_SOURCE)
        doc = parse_code_file(r_file, "r", "test.R")
        assert doc is not None
        assert doc.language == "r"
        assert doc.file_path == "test.R"

    def test_function_blocks_extracted(self, tmp_path: Path) -> None:
        """Function definitions are extracted as separate blocks."""
        blocks = self._parse_r(tmp_path)
        func_blocks = [b for b in blocks if b.symbol_type == "function"]
        # add, print.myclass, my_func
        assert len(func_blocks) == 3

    def test_left_arrow_function_name(self, tmp_path: Path) -> None:
        """A function assigned with <- extracts the correct symbol name."""
        blocks = self._parse_r(tmp_path)
        add_block = [b for b in blocks if b.symbol_name == "add"][0]
        assert add_block.symbol_type == "function"

    def test_left_arrow_function_text(self, tmp_path: Path) -> None:
        """The block text for a <- function includes the full assignment."""
        blocks = self._parse_r(tmp_path)
        add_block = [b for b in blocks if b.symbol_name == "add"][0]
        assert "add <- function" in add_block.text
        assert "x + y" in add_block.text

    def test_s3_method_name(self, tmp_path: Path) -> None:
        """An S3 method with dotted name (print.myclass) extracts correctly."""
        blocks = self._parse_r(tmp_path)
        s3_block = [b for b in blocks if b.symbol_name == "print.myclass"][0]
        assert s3_block.symbol_type == "function"

    def test_equals_assignment_function(self, tmp_path: Path) -> None:
        """A function assigned with = is also extracted as a function block."""
        blocks = self._parse_r(tmp_path)
        eq_block = [b for b in blocks if b.symbol_name == "my_func"][0]
        assert eq_block.symbol_type == "function"
        assert "my_func = function" in eq_block.text

    def test_non_function_assignment_is_top_level(self, tmp_path: Path) -> None:
        """A non-function assignment (threshold <- 0.05) is not a function block."""
        blocks = self._parse_r(tmp_path)
        func_names = [b.symbol_name for b in blocks if b.symbol_type == "function"]
        assert "threshold" not in func_names

    def test_top_level_calls_not_split(self, tmp_path: Path) -> None:
        """Top-level calls like library() are not split into separate blocks."""
        blocks = self._parse_r(tmp_path)
        func_names = [b.symbol_name for b in blocks if b.symbol_type == "function"]
        assert "library" not in func_names

    def test_setrefclass_not_a_function_block(self, tmp_path: Path) -> None:
        """setRefClass assignments are not classified as function blocks."""
        blocks = self._parse_r(tmp_path)
        func_names = [b.symbol_name for b in blocks if b.symbol_type == "function"]
        assert "MyClass" not in func_names

    def test_top_level_block_present(self, tmp_path: Path) -> None:
        """Non-function code is gathered into module_top blocks."""
        blocks = self._parse_r(tmp_path)
        top_blocks = [b for b in blocks if b.symbol_type == "module_top"]
        assert len(top_blocks) >= 1

    def test_start_end_lines_1_based(self, tmp_path: Path) -> None:
        """start_line and end_line use 1-based line numbers."""
        blocks = self._parse_r(tmp_path)
        for block in blocks:
            assert block.start_line >= 1
            assert block.end_line >= block.start_line

    def test_file_path_propagated(self, tmp_path: Path) -> None:
        """The relative file_path is propagated to all blocks."""
        blocks = self._parse_r(tmp_path)
        for block in blocks:
            assert block.file_path == "test.R"

    def test_language_set_on_blocks(self, tmp_path: Path) -> None:
        """All blocks have language set to 'r'."""
        blocks = self._parse_r(tmp_path)
        for block in blocks:
            assert block.language == "r"

    def test_empty_file_produces_no_blocks(self, tmp_path: Path) -> None:
        """An empty .R file produces no blocks."""
        r_file = tmp_path / "empty.R"
        r_file.write_text("")
        doc = parse_code_file(r_file, "r", "empty.R")
        assert doc is not None
        assert len(doc.blocks) == 0

    def test_function_only_file(self, tmp_path: Path) -> None:
        """A file with only function definitions has no module_top blocks."""
        source = """\
add <- function(x, y) {
  x + y
}

mul <- function(x, y) {
  x * y
}
"""
        blocks = self._parse_r(tmp_path, source)
        func_blocks = [b for b in blocks if b.symbol_type == "function"]
        top_blocks = [b for b in blocks if b.symbol_type == "module_top"]
        assert len(func_blocks) == 2
        assert len(top_blocks) == 0

    def test_right_arrow_assignment_not_function(self, tmp_path: Path) -> None:
        """Right-arrow assignment (value -> name) is not split as a function."""
        source = """\
42 -> answer
"""
        blocks = self._parse_r(tmp_path, source)
        func_blocks = [b for b in blocks if b.symbol_type == "function"]
        assert len(func_blocks) == 0


class TestRExtensionMap:
    """Tests for R file extension and language detection."""

    def test_r_lowercase_is_code_file(self) -> None:
        assert is_code_file(Path("script.r")) is True

    def test_r_uppercase_is_code_file(self) -> None:
        assert is_code_file(Path("script.R")) is True

    def test_rmd_is_code_file(self) -> None:
        assert is_code_file(Path("notebook.Rmd")) is True

    def test_rmd_lowercase_is_code_file(self) -> None:
        assert is_code_file(Path("notebook.rmd")) is True

    def test_r_lowercase_returns_r(self) -> None:
        assert get_language(Path("script.r")) == "r"

    def test_r_uppercase_returns_r(self) -> None:
        assert get_language(Path("script.R")) == "r"

    def test_rmd_returns_r(self) -> None:
        assert get_language(Path("notebook.Rmd")) == "r"

    def test_rmd_lowercase_returns_r(self) -> None:
        assert get_language(Path("notebook.rmd")) == "r"


class TestLuaExtensionAndLanguage:
    """Tests for Lua extension mapping and language detection."""

    def test_lua_is_code_file(self) -> None:
        assert is_code_file(Path("init.lua")) is True

    def test_lua_returns_lua(self) -> None:
        assert get_language(Path("init.lua")) == "lua"

    def test_lua_case_insensitive(self) -> None:
        assert is_code_file(Path("script.LUA")) is True
        assert get_language(Path("script.LUA")) == "lua"


class TestLuaParsing:
    """Tests for Lua code parsing via parse_code_file."""

    # A comprehensive Lua source file covering major declaration patterns
    LUA_SOURCE = """\
local function helper(x)
    return x * 2
end

function greet(name)
    print("Hello, " .. name)
end

local M = {}

function M.method(self, arg)
    return self.value + arg
end

function M:otherMethod(arg)
    return self.value - arg
end

local anonymous = function(a, b)
    return a + b
end
"""

    def _parse_lua(self, tmp_path: Path, source: str | None = None) -> list:
        """Write Lua source to a temp file and parse it, returning blocks."""
        lua_file = tmp_path / "test.lua"
        lua_file.write_text(source if source is not None else self.LUA_SOURCE)
        doc = parse_code_file(lua_file, "lua", "test.lua")
        assert doc is not None, "parse_code_file returned None"
        return doc.blocks

    def test_parses_without_error(self, tmp_path: Path) -> None:
        """Lua source parses successfully and returns a CodeDocument."""
        lua_file = tmp_path / "test.lua"
        lua_file.write_text(self.LUA_SOURCE)
        doc = parse_code_file(lua_file, "lua", "test.lua")
        assert doc is not None
        assert doc.language == "lua"
        assert doc.file_path == "test.lua"

    def test_block_count(self, tmp_path: Path) -> None:
        """Lua source produces the expected number of structural blocks.

        Expected blocks:
        1. local function helper (function)
        2. function greet (function)
        3. local M = {} (variable)
        4. function M.method (method)
        5. function M:otherMethod (method)
        6. local anonymous = function(...) (function)
        """
        blocks = self._parse_lua(tmp_path)
        assert len(blocks) == 6

    def test_local_function_name(self, tmp_path: Path) -> None:
        """A local function declaration extracts the correct symbol name."""
        blocks = self._parse_lua(tmp_path)
        local_fn = blocks[0]
        assert local_fn.symbol_name == "helper"

    def test_local_function_type(self, tmp_path: Path) -> None:
        """A local function declaration is classified as 'function'."""
        blocks = self._parse_lua(tmp_path)
        local_fn = blocks[0]
        assert local_fn.symbol_type == "function"

    def test_local_function_text(self, tmp_path: Path) -> None:
        """A local function block contains the full function text."""
        blocks = self._parse_lua(tmp_path)
        local_fn = blocks[0]
        assert "local function helper" in local_fn.text
        assert "return x * 2" in local_fn.text
        assert "end" in local_fn.text

    def test_global_function_name(self, tmp_path: Path) -> None:
        """A global function declaration extracts the correct symbol name."""
        blocks = self._parse_lua(tmp_path)
        global_fn = blocks[1]
        assert global_fn.symbol_name == "greet"

    def test_global_function_type(self, tmp_path: Path) -> None:
        """A global function declaration is classified as 'function'."""
        blocks = self._parse_lua(tmp_path)
        global_fn = blocks[1]
        assert global_fn.symbol_type == "function"

    def test_local_variable_declaration(self, tmp_path: Path) -> None:
        """A local variable (table constructor) is classified as 'variable'."""
        blocks = self._parse_lua(tmp_path)
        var_block = blocks[2]
        assert var_block.symbol_name == "M"
        assert var_block.symbol_type == "variable"

    def test_dot_method_name(self, tmp_path: Path) -> None:
        """A dot-style method (M.method) extracts the full dotted name."""
        blocks = self._parse_lua(tmp_path)
        dot_method = blocks[3]
        assert dot_method.symbol_name == "M.method"

    def test_dot_method_type(self, tmp_path: Path) -> None:
        """A dot-style method is classified as 'method'."""
        blocks = self._parse_lua(tmp_path)
        dot_method = blocks[3]
        assert dot_method.symbol_type == "method"

    def test_colon_method_name(self, tmp_path: Path) -> None:
        """A colon-style method (M:otherMethod) extracts the full name."""
        blocks = self._parse_lua(tmp_path)
        colon_method = blocks[4]
        assert colon_method.symbol_name == "M:otherMethod"

    def test_colon_method_type(self, tmp_path: Path) -> None:
        """A colon-style method is classified as 'method'."""
        blocks = self._parse_lua(tmp_path)
        colon_method = blocks[4]
        assert colon_method.symbol_type == "method"

    def test_anonymous_function_name(self, tmp_path: Path) -> None:
        """An anonymous function assigned to a local extracts the variable name."""
        blocks = self._parse_lua(tmp_path)
        anon_fn = blocks[5]
        assert anon_fn.symbol_name == "anonymous"

    def test_anonymous_function_type(self, tmp_path: Path) -> None:
        """An anonymous function assigned to a local is classified as 'function'."""
        blocks = self._parse_lua(tmp_path)
        anon_fn = blocks[5]
        assert anon_fn.symbol_type == "function"

    def test_start_end_lines_1_based(self, tmp_path: Path) -> None:
        """start_line and end_line use 1-based line numbers."""
        blocks = self._parse_lua(tmp_path)
        for block in blocks:
            assert block.start_line >= 1
            assert block.end_line >= block.start_line

    def test_file_path_propagated(self, tmp_path: Path) -> None:
        """The relative file_path is propagated to all blocks."""
        blocks = self._parse_lua(tmp_path)
        for block in blocks:
            assert block.file_path == "test.lua"

    def test_language_set_on_blocks(self, tmp_path: Path) -> None:
        """All blocks have language set to 'lua'."""
        blocks = self._parse_lua(tmp_path)
        for block in blocks:
            assert block.language == "lua"

    def test_empty_file_produces_no_blocks(self, tmp_path: Path) -> None:
        """An empty .lua file produces no blocks."""
        lua_file = tmp_path / "empty.lua"
        lua_file.write_text("")
        doc = parse_code_file(lua_file, "lua", "empty.lua")
        assert doc is not None
        assert len(doc.blocks) == 0

    def test_only_comments_produces_single_block(self, tmp_path: Path) -> None:
        """A file with only comments produces a single module_top block."""
        source = """\
-- This is a comment
-- Another comment
"""
        blocks = self._parse_lua(tmp_path, source)
        assert len(blocks) == 1
        assert blocks[0].symbol_type == "module_top"
        assert blocks[0].symbol_name == "(top-level)"

    def test_method_text_contains_body(self, tmp_path: Path) -> None:
        """A method block's text contains the full function body."""
        blocks = self._parse_lua(tmp_path)
        dot_method = blocks[3]
        assert "function M.method" in dot_method.text
        assert "self.value + arg" in dot_method.text
        assert "end" in dot_method.text


class TestPerlParsing:
    """Tests for Perl code parsing via parse_code_file."""

    # A comprehensive Perl source file covering major declaration types
    PERL_SOURCE = """\
package MyModule;
use strict;
use warnings;

sub new {
    my ($class, %args) = @_;
    return bless \\%args, $class;
}

sub greet {
    my ($self) = @_;
    return "Hello, " . $self->{name};
}

my $helper = sub {
    return 42;
};

sub _private_method {
    my ($self) = @_;
    return $self->{internal};
}

1;
"""

    def _parse_perl(self, tmp_path: Path, source: str | None = None) -> list:
        """Write Perl source to a temp file and parse it, returning blocks."""
        perl_file = tmp_path / "test.pm"
        perl_file.write_text(source if source is not None else self.PERL_SOURCE)
        doc = parse_code_file(perl_file, "perl", "test.pm")
        assert doc is not None, "parse_code_file returned None"
        return doc.blocks

    def test_parses_without_error(self, tmp_path: Path) -> None:
        """Perl source parses successfully and returns a CodeDocument."""
        perl_file = tmp_path / "test.pm"
        perl_file.write_text(self.PERL_SOURCE)
        doc = parse_code_file(perl_file, "perl", "test.pm")
        assert doc is not None
        assert doc.language == "perl"
        assert doc.file_path == "test.pm"

    def test_block_count(self, tmp_path: Path) -> None:
        """Perl source produces the expected number of structural blocks.

        Expected blocks:
        1. package MyModule (package)
        2. use strict; use warnings; (top-level)
        3. sub new (subroutine)
        4. sub greet (subroutine)
        5. my $helper = sub { ... }; (top-level -- anonymous sub not split)
        6. sub _private_method (subroutine)
        7. 1; (top-level)
        """
        blocks = self._parse_perl(tmp_path)
        assert len(blocks) == 7

    def test_package_declaration(self, tmp_path: Path) -> None:
        """A package statement is classified as 'package' with the package name."""
        blocks = self._parse_perl(tmp_path)
        pkg_block = blocks[0]
        assert pkg_block.symbol_name == "MyModule"
        assert pkg_block.symbol_type == "package"
        assert "package MyModule" in pkg_block.text

    def test_subroutine_new(self, tmp_path: Path) -> None:
        """The 'new' subroutine is parsed with correct name and type."""
        blocks = self._parse_perl(tmp_path)
        sub_new = blocks[2]
        assert sub_new.symbol_name == "new"
        assert sub_new.symbol_type == "subroutine"

    def test_subroutine_greet(self, tmp_path: Path) -> None:
        """The 'greet' subroutine is parsed with correct name and type."""
        blocks = self._parse_perl(tmp_path)
        sub_greet = blocks[3]
        assert sub_greet.symbol_name == "greet"
        assert sub_greet.symbol_type == "subroutine"

    def test_private_subroutine(self, tmp_path: Path) -> None:
        """A private subroutine (prefixed with _) is correctly parsed."""
        blocks = self._parse_perl(tmp_path)
        sub_private = blocks[5]
        assert sub_private.symbol_name == "_private_method"
        assert sub_private.symbol_type == "subroutine"

    def test_anonymous_sub_in_top_level(self, tmp_path: Path) -> None:
        """An anonymous sub assigned to a variable is part of a top-level block."""
        blocks = self._parse_perl(tmp_path)
        # The anonymous sub is in a top-level expression_statement, not split
        anon_block = blocks[4]
        assert anon_block.symbol_type == "module_top"
        assert "sub" in anon_block.text

    def test_use_statements_in_top_level(self, tmp_path: Path) -> None:
        """use statements are accumulated into a top-level block."""
        blocks = self._parse_perl(tmp_path)
        use_block = blocks[1]
        assert use_block.symbol_type == "module_top"
        assert "use strict" in use_block.text
        assert "use warnings" in use_block.text

    def test_start_end_lines_1_based(self, tmp_path: Path) -> None:
        """start_line and end_line use 1-based line numbers."""
        blocks = self._parse_perl(tmp_path)
        for block in blocks:
            assert block.start_line >= 1
            assert block.end_line >= block.start_line

    def test_file_path_propagated(self, tmp_path: Path) -> None:
        """The relative file_path is propagated to all blocks."""
        blocks = self._parse_perl(tmp_path)
        for block in blocks:
            assert block.file_path == "test.pm"

    def test_language_set_on_blocks(self, tmp_path: Path) -> None:
        """All blocks have language set to 'perl'."""
        blocks = self._parse_perl(tmp_path)
        for block in blocks:
            assert block.language == "perl"

    def test_multiple_packages(self, tmp_path: Path) -> None:
        """Multiple package declarations are each split into separate blocks."""
        source = """\
package First;
sub foo { }
package Second;
sub bar { }
1;
"""
        blocks = self._parse_perl(tmp_path, source)
        pkg_blocks = [b for b in blocks if b.symbol_type == "package"]
        assert len(pkg_blocks) == 2
        assert pkg_blocks[0].symbol_name == "First"
        assert pkg_blocks[1].symbol_name == "Second"

    def test_namespaced_package(self, tmp_path: Path) -> None:
        """A namespaced package name (e.g. Foo::Bar) is correctly extracted."""
        source = """\
package Foo::Bar::Baz;
sub method { }
1;
"""
        blocks = self._parse_perl(tmp_path, source)
        pkg_block = [b for b in blocks if b.symbol_type == "package"][0]
        assert pkg_block.symbol_name == "Foo::Bar::Baz"

    def test_empty_file_produces_no_blocks(self, tmp_path: Path) -> None:
        """An empty .pl file produces no blocks."""
        perl_file = tmp_path / "empty.pl"
        perl_file.write_text("")
        doc = parse_code_file(perl_file, "perl", "empty.pl")
        assert doc is not None
        assert len(doc.blocks) == 0

    def test_sub_text_contains_body(self, tmp_path: Path) -> None:
        """A subroutine block's text contains the full sub body."""
        blocks = self._parse_perl(tmp_path)
        sub_new = blocks[2]
        assert "sub new" in sub_new.text
        assert "bless" in sub_new.text


class TestKotlinParsing:
    """Tests for Kotlin code parsing via parse_code_file."""

    KOTLIN_SOURCE = """\
package com.example

class MyClass {
    fun greet(name: String): String {
        return "Hello, $name"
    }

    companion object {
        const val VERSION = "1.0"
    }
}

interface Greeter {
    fun greet(): String
}

enum class Color {
    RED, GREEN, BLUE
}

fun topLevelFunction(): Int {
    return 42
}

data class Point(val x: Int, val y: Int)

object Singleton {
    fun doStuff() {}
}
"""

    def _parse_kotlin(self, tmp_path: Path, source: str | None = None) -> list:
        """Write Kotlin source to a temp file and parse it, returning blocks."""
        kt_file = tmp_path / "test.kt"
        kt_file.write_text(source if source is not None else self.KOTLIN_SOURCE)
        doc = parse_code_file(kt_file, "kotlin", "test.kt")
        assert doc is not None, "parse_code_file returned None"
        return doc.blocks

    def test_kotlin_is_code_file(self) -> None:
        assert is_code_file(Path("Main.kt")) is True

    def test_kotlin_returns_kotlin(self) -> None:
        assert get_language(Path("Main.kt")) == "kotlin"

    def test_parses_without_error(self, tmp_path: Path) -> None:
        kt_file = tmp_path / "test.kt"
        kt_file.write_text(self.KOTLIN_SOURCE)
        doc = parse_code_file(kt_file, "kotlin", "test.kt")
        assert doc is not None
        assert doc.language == "kotlin"
        assert doc.file_path == "test.kt"

    def test_block_count(self, tmp_path: Path) -> None:
        blocks = self._parse_kotlin(tmp_path)
        assert len(blocks) == 7

    def test_top_level_package_declaration(self, tmp_path: Path) -> None:
        blocks = self._parse_kotlin(tmp_path)
        top = blocks[0]
        assert top.symbol_type == "module_top"
        assert "package" in top.text

    def test_class_declaration(self, tmp_path: Path) -> None:
        blocks = self._parse_kotlin(tmp_path)
        cls = [b for b in blocks if b.symbol_name == "MyClass"][0]
        assert cls.symbol_type == "class"
        assert "fun greet" in cls.text

    def test_interface_declaration(self, tmp_path: Path) -> None:
        blocks = self._parse_kotlin(tmp_path)
        iface = [b for b in blocks if b.symbol_name == "Greeter"][0]
        assert iface.symbol_type == "interface"

    def test_enum_class_declaration(self, tmp_path: Path) -> None:
        blocks = self._parse_kotlin(tmp_path)
        enum = [b for b in blocks if b.symbol_name == "Color"][0]
        assert enum.symbol_type == "enum"

    def test_top_level_function(self, tmp_path: Path) -> None:
        blocks = self._parse_kotlin(tmp_path)
        func = [b for b in blocks if b.symbol_name == "topLevelFunction"][0]
        assert func.symbol_type == "function"
        assert "return 42" in func.text

    def test_data_class_declaration(self, tmp_path: Path) -> None:
        blocks = self._parse_kotlin(tmp_path)
        dc = [b for b in blocks if b.symbol_name == "Point"][0]
        assert dc.symbol_type == "data_class"

    def test_object_declaration(self, tmp_path: Path) -> None:
        blocks = self._parse_kotlin(tmp_path)
        obj = [b for b in blocks if b.symbol_name == "Singleton"][0]
        assert obj.symbol_type == "object"
        assert "fun doStuff" in obj.text

    def test_companion_object_not_split(self, tmp_path: Path) -> None:
        blocks = self._parse_kotlin(tmp_path)
        companions = [b for b in blocks if b.symbol_name == "companion"]
        assert len(companions) == 0

    def test_symbol_names(self, tmp_path: Path) -> None:
        blocks = self._parse_kotlin(tmp_path)
        names = {b.symbol_name for b in blocks if b.symbol_type != "module_top"}
        assert names == {
            "MyClass",
            "Greeter",
            "Color",
            "topLevelFunction",
            "Point",
            "Singleton",
        }

    def test_start_end_lines_1_based(self, tmp_path: Path) -> None:
        blocks = self._parse_kotlin(tmp_path)
        for block in blocks:
            assert block.start_line >= 1
            assert block.end_line >= block.start_line

    def test_file_path_propagated(self, tmp_path: Path) -> None:
        blocks = self._parse_kotlin(tmp_path)
        for block in blocks:
            assert block.file_path == "test.kt"

    def test_language_set_on_blocks(self, tmp_path: Path) -> None:
        blocks = self._parse_kotlin(tmp_path)
        for block in blocks:
            assert block.language == "kotlin"

    def test_sealed_class(self, tmp_path: Path) -> None:
        source = "sealed class Result {}\n"
        blocks = self._parse_kotlin(tmp_path, source)
        assert len(blocks) == 1
        assert blocks[0].symbol_name == "Result"
        assert blocks[0].symbol_type == "class"

    def test_abstract_class(self, tmp_path: Path) -> None:
        source = "abstract class Base {}\n"
        blocks = self._parse_kotlin(tmp_path, source)
        assert len(blocks) == 1
        assert blocks[0].symbol_name == "Base"
        assert blocks[0].symbol_type == "class"

    def test_annotation_class(self, tmp_path: Path) -> None:
        source = "annotation class MyAnnotation\n"
        blocks = self._parse_kotlin(tmp_path, source)
        assert len(blocks) == 1
        assert blocks[0].symbol_name == "MyAnnotation"
        assert blocks[0].symbol_type == "class"

    def test_empty_file_produces_no_blocks(self, tmp_path: Path) -> None:
        kt_file = tmp_path / "empty.kt"
        kt_file.write_text("")
        doc = parse_code_file(kt_file, "kotlin", "empty.kt")
        assert doc is not None
        assert len(doc.blocks) == 0

    def test_function_only_file(self, tmp_path: Path) -> None:
        source = 'fun main() { println("Hello") }\n'
        blocks = self._parse_kotlin(tmp_path, source)
        assert len(blocks) == 1
        assert blocks[0].symbol_name == "main"
        assert blocks[0].symbol_type == "function"

    def test_class_start_end_lines(self, tmp_path: Path) -> None:
        blocks = self._parse_kotlin(tmp_path)
        cls = [b for b in blocks if b.symbol_name == "MyClass"][0]
        assert cls.start_line == 3
        assert cls.end_line == 11

    def test_function_start_end_lines(self, tmp_path: Path) -> None:
        blocks = self._parse_kotlin(tmp_path)
        func = [b for b in blocks if b.symbol_name == "topLevelFunction"][0]
        assert func.start_line == 21
        assert func.end_line == 23


class TestPhpExtensionAndLanguage:
    """Tests for PHP extension mapping and language detection."""

    def test_php_is_code_file(self) -> None:
        assert is_code_file(Path("index.php")) is True

    def test_php_returns_php(self) -> None:
        assert get_language(Path("index.php")) == "php"

    def test_php_case_insensitive(self) -> None:
        assert is_code_file(Path("index.PHP")) is True
        assert get_language(Path("index.PHP")) == "php"


class TestPhpParsing:
    """Tests for PHP code parsing via parse_code_file."""

    PHP_SOURCE = (
        "<?php\n"
        "namespace App\\\\Models;\n"
        "\n"
        "class User {\n"
        "    private string $name;\n"
        "\n"
        "    public function __construct(string $name) {\n"
        "        $this->name = $name;\n"
        "    }\n"
        "\n"
        "    public function getName(): string {\n"
        "        return $this->name;\n"
        "    }\n"
        "}\n"
        "\n"
        "interface Authenticatable {\n"
        "    public function authenticate(): bool;\n"
        "}\n"
        "\n"
        "trait HasTimestamps {\n"
        "    public function createdAt(): DateTime {\n"
        "        return $this->created_at;\n"
        "    }\n"
        "}\n"
        "\n"
        "enum Status: string {\n"
        "    case Active = 'active';\n"
        "    case Inactive = 'inactive';\n"
        "}\n"
        "\n"
        "function helper(): void {\n"
        '    echo "hello";\n'
        "}\n"
    )

    def _parse_php(self, tmp_path: Path, source: str | None = None) -> list:
        """Write PHP source to a temp file and parse it, returning blocks."""
        php_file = tmp_path / "test.php"
        php_file.write_text(source if source is not None else self.PHP_SOURCE)
        doc = parse_code_file(php_file, "php", "test.php")
        assert doc is not None, "parse_code_file returned None"
        return doc.blocks

    def test_parses_without_error(self, tmp_path: Path) -> None:
        """PHP source parses successfully and returns a CodeDocument."""
        php_file = tmp_path / "test.php"
        php_file.write_text(self.PHP_SOURCE)
        doc = parse_code_file(php_file, "php", "test.php")
        assert doc is not None
        assert doc.language == "php"
        assert doc.file_path == "test.php"

    def test_block_count(self, tmp_path: Path) -> None:
        """PHP source produces the expected number of structural blocks.

        Expected blocks:
        1. namespace App\\Models (top-level, non-split)
        2. class User
        3. interface Authenticatable
        4. trait HasTimestamps
        5. enum Status
        6. function helper
        """
        blocks = self._parse_php(tmp_path)
        split_blocks = [b for b in blocks if b.symbol_type != "module_top"]
        assert len(split_blocks) == 5

    def test_class_symbol_name(self, tmp_path: Path) -> None:
        """A class declaration extracts the correct symbol name."""
        blocks = self._parse_php(tmp_path)
        class_block = [b for b in blocks if b.symbol_name == "User"][0]
        assert class_block.symbol_name == "User"

    def test_class_symbol_type(self, tmp_path: Path) -> None:
        """A class declaration is classified as symbol_type 'class'."""
        blocks = self._parse_php(tmp_path)
        class_block = [b for b in blocks if b.symbol_name == "User"][0]
        assert class_block.symbol_type == "class"

    def test_class_text_contains_methods(self, tmp_path: Path) -> None:
        """The class block text includes method definitions."""
        blocks = self._parse_php(tmp_path)
        class_block = [b for b in blocks if b.symbol_name == "User"][0]
        assert "class User" in class_block.text
        assert "__construct" in class_block.text
        assert "getName" in class_block.text

    def test_interface_symbol_name(self, tmp_path: Path) -> None:
        """An interface declaration extracts the correct symbol name."""
        blocks = self._parse_php(tmp_path)
        iface_block = [b for b in blocks if b.symbol_name == "Authenticatable"][0]
        assert iface_block.symbol_name == "Authenticatable"

    def test_interface_symbol_type(self, tmp_path: Path) -> None:
        """An interface declaration is classified as symbol_type 'interface'."""
        blocks = self._parse_php(tmp_path)
        iface_block = [b for b in blocks if b.symbol_name == "Authenticatable"][0]
        assert iface_block.symbol_type == "interface"

    def test_trait_symbol_name(self, tmp_path: Path) -> None:
        """A trait declaration extracts the correct symbol name."""
        blocks = self._parse_php(tmp_path)
        trait_block = [b for b in blocks if b.symbol_name == "HasTimestamps"][0]
        assert trait_block.symbol_name == "HasTimestamps"

    def test_trait_symbol_type(self, tmp_path: Path) -> None:
        """A trait declaration is classified as symbol_type 'trait'."""
        blocks = self._parse_php(tmp_path)
        trait_block = [b for b in blocks if b.symbol_name == "HasTimestamps"][0]
        assert trait_block.symbol_type == "trait"

    def test_enum_symbol_name(self, tmp_path: Path) -> None:
        """An enum declaration extracts the correct symbol name."""
        blocks = self._parse_php(tmp_path)
        enum_block = [b for b in blocks if b.symbol_name == "Status"][0]
        assert enum_block.symbol_name == "Status"

    def test_enum_symbol_type(self, tmp_path: Path) -> None:
        """An enum declaration is classified as symbol_type 'enum'."""
        blocks = self._parse_php(tmp_path)
        enum_block = [b for b in blocks if b.symbol_name == "Status"][0]
        assert enum_block.symbol_type == "enum"

    def test_function_symbol_name(self, tmp_path: Path) -> None:
        """A function definition extracts the correct symbol name."""
        blocks = self._parse_php(tmp_path)
        fn_block = [b for b in blocks if b.symbol_name == "helper"][0]
        assert fn_block.symbol_name == "helper"

    def test_function_symbol_type(self, tmp_path: Path) -> None:
        """A function definition is classified as symbol_type 'function'."""
        blocks = self._parse_php(tmp_path)
        fn_block = [b for b in blocks if b.symbol_name == "helper"][0]
        assert fn_block.symbol_type == "function"

    def test_function_text_contains_body(self, tmp_path: Path) -> None:
        """The function block text includes the full function body."""
        blocks = self._parse_php(tmp_path)
        fn_block = [b for b in blocks if b.symbol_name == "helper"][0]
        assert "function helper" in fn_block.text
        assert "echo" in fn_block.text

    def test_namespace_in_top_level(self, tmp_path: Path) -> None:
        """Namespace and php_tag end up in module_top block."""
        blocks = self._parse_php(tmp_path)
        top_blocks = [b for b in blocks if b.symbol_type == "module_top"]
        assert len(top_blocks) >= 1
        top_text = " ".join(b.text for b in top_blocks)
        assert "namespace" in top_text

    def test_start_end_lines_1_based(self, tmp_path: Path) -> None:
        """start_line and end_line use 1-based line numbers."""
        blocks = self._parse_php(tmp_path)
        for block in blocks:
            assert block.start_line >= 1
            assert block.end_line >= block.start_line

    def test_file_path_propagated(self, tmp_path: Path) -> None:
        """The relative file_path is propagated to all blocks."""
        blocks = self._parse_php(tmp_path)
        for block in blocks:
            assert block.file_path == "test.php"

    def test_language_set_on_blocks(self, tmp_path: Path) -> None:
        """All blocks have language set to 'php'."""
        blocks = self._parse_php(tmp_path)
        for block in blocks:
            assert block.language == "php"

    def test_abstract_class(self, tmp_path: Path) -> None:
        """An abstract class declaration is correctly parsed."""
        source = (
            "<?php\nabstract class BaseModel {\n    abstract public function save(): void;\n}\n"
        )
        blocks = self._parse_php(tmp_path, source)
        class_blocks = [b for b in blocks if b.symbol_type == "class"]
        assert len(class_blocks) == 1
        assert class_blocks[0].symbol_name == "BaseModel"
        assert "abstract class" in class_blocks[0].text

    def test_final_class(self, tmp_path: Path) -> None:
        """A final class declaration is correctly parsed."""
        source = "<?php\nfinal class Config {\n    public static string $version = '1.0';\n}\n"
        blocks = self._parse_php(tmp_path, source)
        class_blocks = [b for b in blocks if b.symbol_type == "class"]
        assert len(class_blocks) == 1
        assert class_blocks[0].symbol_name == "Config"

    def test_empty_file_produces_no_blocks(self, tmp_path: Path) -> None:
        """An empty .php file produces no blocks."""
        php_file = tmp_path / "empty.php"
        php_file.write_text("")
        doc = parse_code_file(php_file, "php", "empty.php")
        assert doc is not None
        assert len(doc.blocks) == 0

    def test_only_functions(self, tmp_path: Path) -> None:
        """A file with only functions parses correctly."""
        source = (
            "<?php\n"
            "function first(): void {\n"
            '    echo "first";\n'
            "}\n"
            "\n"
            "function second(): void {\n"
            '    echo "second";\n'
            "}\n"
        )
        blocks = self._parse_php(tmp_path, source)
        fn_blocks = [b for b in blocks if b.symbol_type == "function"]
        assert len(fn_blocks) == 2
        assert fn_blocks[0].symbol_name == "first"
        assert fn_blocks[1].symbol_name == "second"

    def test_top_level_code_becomes_module_top(self, tmp_path: Path) -> None:
        """Top-level code outside declarations becomes module_top."""
        source = (
            "<?php\n"
            "require_once 'vendor/autoload.php';\n"
            "$app = new Application();\n"
            "\n"
            "function run(): void {\n"
            '    echo "running";\n'
            "}\n"
        )
        blocks = self._parse_php(tmp_path, source)
        top_blocks = [b for b in blocks if b.symbol_type == "module_top"]
        fn_blocks = [b for b in blocks if b.symbol_type == "function"]
        assert len(top_blocks) >= 1
        assert len(fn_blocks) == 1
        top_text = " ".join(b.text for b in top_blocks)
        assert "require_once" in top_text


class TestScalaExtensionAndLanguage:
    """Tests for Scala file extension mapping and language detection."""

    def test_scala_is_code_file(self) -> None:
        assert is_code_file(Path("Main.scala")) is True

    def test_sc_is_code_file(self) -> None:
        assert is_code_file(Path("script.sc")) is True

    def test_scala_returns_scala(self) -> None:
        assert get_language(Path("Main.scala")) == "scala"

    def test_sc_returns_scala(self) -> None:
        assert get_language(Path("script.sc")) == "scala"


class TestScalaParsing:
    """Tests for Scala code parsing via parse_code_file."""

    # A comprehensive Scala source file covering all major declaration types
    SCALA_SOURCE = """\
package com.example

import scala.collection.mutable

class Animal(val name: String) {
  def speak(): String = "..."
}

case class Point(x: Double, y: Double)

object Singleton {
  def doStuff(): Unit = println("stuff")
}

trait Drawable {
  def draw(): Unit
}

sealed trait Shape
case class Circle(radius: Double) extends Shape
case class Rectangle(w: Double, h: Double) extends Shape

def topLevel(): Int = 42

enum Color {
  case Red, Green, Blue
}
"""

    def _parse_scala(self, tmp_path: Path, source: str | None = None) -> list:
        """Write Scala source to a temp file and parse it, returning blocks."""
        scala_file = tmp_path / "test.scala"
        scala_file.write_text(source if source is not None else self.SCALA_SOURCE)
        doc = parse_code_file(scala_file, "scala", "test.scala")
        assert doc is not None, "parse_code_file returned None"
        return doc.blocks

    def test_parses_without_error(self, tmp_path: Path) -> None:
        """Scala source parses successfully and returns a CodeDocument."""
        scala_file = tmp_path / "test.scala"
        scala_file.write_text(self.SCALA_SOURCE)
        doc = parse_code_file(scala_file, "scala", "test.scala")
        assert doc is not None
        assert doc.language == "scala"
        assert doc.file_path == "test.scala"

    def test_block_count(self, tmp_path: Path) -> None:
        """Scala source produces the expected number of structural blocks.

        Expected blocks:
        1. package + import (module_top)
        2. class Animal (class)
        3. case class Point (class)
        4. object Singleton (object)
        5. trait Drawable (trait)
        6. sealed trait Shape (trait)
        7. case class Circle (class)
        8. case class Rectangle (class)
        9. def topLevel (function)
        10. enum Color (enum)
        """
        blocks = self._parse_scala(tmp_path)
        assert len(blocks) == 10

    def test_package_and_import_in_top_level(self, tmp_path: Path) -> None:
        """Package and import statements are grouped into a module_top block."""
        blocks = self._parse_scala(tmp_path)
        top_block = blocks[0]
        assert top_block.symbol_type == "module_top"
        assert top_block.symbol_name == "(top-level)"
        assert "package" in top_block.text
        assert "import" in top_block.text

    def test_class_definition(self, tmp_path: Path) -> None:
        """A class definition is correctly parsed with symbol_type 'class'."""
        blocks = self._parse_scala(tmp_path)
        animal = blocks[1]
        assert animal.symbol_name == "Animal"
        assert animal.symbol_type == "class"
        assert "class Animal" in animal.text

    def test_case_class_definition(self, tmp_path: Path) -> None:
        """A case class is parsed as symbol_type 'class'."""
        blocks = self._parse_scala(tmp_path)
        point = blocks[2]
        assert point.symbol_name == "Point"
        assert point.symbol_type == "class"
        assert "case class" in point.text

    def test_object_definition(self, tmp_path: Path) -> None:
        """An object definition is parsed with symbol_type 'object'."""
        blocks = self._parse_scala(tmp_path)
        singleton = blocks[3]
        assert singleton.symbol_name == "Singleton"
        assert singleton.symbol_type == "object"
        assert "object Singleton" in singleton.text

    def test_trait_definition(self, tmp_path: Path) -> None:
        """A trait definition is parsed with symbol_type 'trait'."""
        blocks = self._parse_scala(tmp_path)
        drawable = blocks[4]
        assert drawable.symbol_name == "Drawable"
        assert drawable.symbol_type == "trait"

    def test_sealed_trait_definition(self, tmp_path: Path) -> None:
        """A sealed trait is parsed with symbol_type 'trait'."""
        blocks = self._parse_scala(tmp_path)
        shape = blocks[5]
        assert shape.symbol_name == "Shape"
        assert shape.symbol_type == "trait"
        assert "sealed" in shape.text

    def test_case_class_extends(self, tmp_path: Path) -> None:
        """Case classes extending a trait are parsed correctly."""
        blocks = self._parse_scala(tmp_path)
        circle = blocks[6]
        assert circle.symbol_name == "Circle"
        assert circle.symbol_type == "class"
        assert "extends Shape" in circle.text

    def test_top_level_function(self, tmp_path: Path) -> None:
        """A top-level def is parsed with symbol_type 'function'."""
        blocks = self._parse_scala(tmp_path)
        top_fn = blocks[8]
        assert top_fn.symbol_name == "topLevel"
        assert top_fn.symbol_type == "function"

    def test_enum_definition(self, tmp_path: Path) -> None:
        """An enum definition (Scala 3) is parsed with symbol_type 'enum'."""
        blocks = self._parse_scala(tmp_path)
        color = blocks[9]
        assert color.symbol_name == "Color"
        assert color.symbol_type == "enum"
        assert "enum Color" in color.text

    def test_val_definition(self, tmp_path: Path) -> None:
        """A top-level val definition is parsed with symbol_type 'val'."""
        source = "val maxSize: Int = 42\n"
        blocks = self._parse_scala(tmp_path, source)
        assert len(blocks) >= 1
        val_block = blocks[0]
        assert val_block.symbol_name == "maxSize"
        assert val_block.symbol_type == "val"

    def test_var_definition(self, tmp_path: Path) -> None:
        """A top-level var definition is parsed with symbol_type 'var'."""
        source = "var counter: Int = 0\n"
        blocks = self._parse_scala(tmp_path, source)
        assert len(blocks) >= 1
        var_block = blocks[0]
        assert var_block.symbol_name == "counter"
        assert var_block.symbol_type == "var"

    def test_type_definition(self, tmp_path: Path) -> None:
        """A type alias is parsed with symbol_type 'type'."""
        source = "type Alias = List[Int]\n"
        blocks = self._parse_scala(tmp_path, source)
        assert len(blocks) >= 1
        type_block = blocks[0]
        assert type_block.symbol_name == "Alias"
        assert type_block.symbol_type == "type"

    def test_given_definition(self, tmp_path: Path) -> None:
        """A given definition (Scala 3) is parsed with symbol_type 'given'."""
        source = "given intOrd: Ordering[Int] = Ordering.Int\n"
        blocks = self._parse_scala(tmp_path, source)
        assert len(blocks) >= 1
        given_block = blocks[0]
        assert given_block.symbol_name == "intOrd"
        assert given_block.symbol_type == "given"

    def test_abstract_def_in_trait(self, tmp_path: Path) -> None:
        """An abstract def (function_declaration) inside a trait is captured
        as part of the trait block, not a separate top-level block."""
        source = """\
trait Foo {
  def bar(): Unit
}
"""
        blocks = self._parse_scala(tmp_path, source)
        # Only 1 block: the trait itself (abstract def is nested inside)
        assert len(blocks) == 1
        assert blocks[0].symbol_type == "trait"
        assert blocks[0].symbol_name == "Foo"
        assert "def bar" in blocks[0].text

    def test_start_end_lines_1_based(self, tmp_path: Path) -> None:
        """start_line and end_line use 1-based line numbers."""
        blocks = self._parse_scala(tmp_path)
        for block in blocks:
            assert block.start_line >= 1
            assert block.end_line >= block.start_line

    def test_file_path_propagated(self, tmp_path: Path) -> None:
        """The relative file_path is propagated to all blocks."""
        blocks = self._parse_scala(tmp_path)
        for block in blocks:
            assert block.file_path == "test.scala"

    def test_language_set_on_blocks(self, tmp_path: Path) -> None:
        """All blocks have language set to 'scala'."""
        blocks = self._parse_scala(tmp_path)
        for block in blocks:
            assert block.language == "scala"

    def test_empty_file_produces_no_blocks(self, tmp_path: Path) -> None:
        """An empty .scala file produces no blocks."""
        scala_file = tmp_path / "empty.scala"
        scala_file.write_text("")
        doc = parse_code_file(scala_file, "scala", "empty.scala")
        assert doc is not None
        assert len(doc.blocks) == 0


class TestElixirParsing:
    """Tests for Elixir code parsing via parse_code_file."""

    # A comprehensive Elixir source file covering all major declaration types
    ELIXIR_SOURCE = """\
defmodule MyApp.User do
  @moduledoc "User module"

  defstruct [:name, :email]

  def new(name, email) do
    %__MODULE__{name: name, email: email}
  end

  defp validate(user) do
    user.name != nil
  end

  defmacro is_admin(user) do
    quote do
      unquote(user).role == :admin
    end
  end
end

defprotocol Printable do
  def to_string(data)
end

defimpl Printable, for: MyApp.User do
  def to_string(user), do: user.name
end
"""

    def _parse_elixir(self, tmp_path: Path, source: str | None = None) -> list:
        """Write Elixir source to a temp file and parse it, returning blocks."""
        ex_file = tmp_path / "test.ex"
        ex_file.write_text(source if source is not None else self.ELIXIR_SOURCE)
        doc = parse_code_file(ex_file, "elixir", "test.ex")
        assert doc is not None, "parse_code_file returned None"
        return doc.blocks

    def test_parses_without_error(self, tmp_path: Path) -> None:
        """Elixir source parses successfully and returns a CodeDocument."""
        ex_file = tmp_path / "test.ex"
        ex_file.write_text(self.ELIXIR_SOURCE)
        doc = parse_code_file(ex_file, "elixir", "test.ex")
        assert doc is not None
        assert doc.language == "elixir"
        assert doc.file_path == "test.ex"

    def test_block_count(self, tmp_path: Path) -> None:
        """Elixir source produces the expected number of structural blocks.

        Expected blocks:
        1. defmodule MyApp.User (module)
        2. defprotocol Printable (protocol)
        3. defimpl Printable (impl)
        """
        blocks = self._parse_elixir(tmp_path)
        assert len(blocks) == 3

    def test_defmodule_symbol_name(self, tmp_path: Path) -> None:
        """A defmodule declaration extracts the module name."""
        blocks = self._parse_elixir(tmp_path)
        mod_block = blocks[0]
        assert mod_block.symbol_name == "MyApp.User"

    def test_defmodule_symbol_type(self, tmp_path: Path) -> None:
        """A defmodule declaration is classified as symbol_type 'module'."""
        blocks = self._parse_elixir(tmp_path)
        mod_block = blocks[0]
        assert mod_block.symbol_type == "module"

    def test_defmodule_contains_functions(self, tmp_path: Path) -> None:
        """The defmodule block text contains nested def/defp/defmacro."""
        blocks = self._parse_elixir(tmp_path)
        mod_block = blocks[0]
        assert "def new" in mod_block.text
        assert "defp validate" in mod_block.text
        assert "defmacro is_admin" in mod_block.text

    def test_defprotocol_symbol_name(self, tmp_path: Path) -> None:
        """A defprotocol declaration extracts the protocol name."""
        blocks = self._parse_elixir(tmp_path)
        proto_block = blocks[1]
        assert proto_block.symbol_name == "Printable"

    def test_defprotocol_symbol_type(self, tmp_path: Path) -> None:
        """A defprotocol declaration is classified as symbol_type 'protocol'."""
        blocks = self._parse_elixir(tmp_path)
        proto_block = blocks[1]
        assert proto_block.symbol_type == "protocol"

    def test_defimpl_symbol_name(self, tmp_path: Path) -> None:
        """A defimpl declaration extracts the implementation target name."""
        blocks = self._parse_elixir(tmp_path)
        impl_block = blocks[2]
        assert impl_block.symbol_name == "Printable"

    def test_defimpl_symbol_type(self, tmp_path: Path) -> None:
        """A defimpl declaration is classified as symbol_type 'impl'."""
        blocks = self._parse_elixir(tmp_path)
        impl_block = blocks[2]
        assert impl_block.symbol_type == "impl"

    def test_start_end_lines_1_based(self, tmp_path: Path) -> None:
        """start_line and end_line use 1-based line numbers."""
        blocks = self._parse_elixir(tmp_path)
        for block in blocks:
            assert block.start_line >= 1
            assert block.end_line >= block.start_line

    def test_file_path_propagated(self, tmp_path: Path) -> None:
        """The relative file_path is propagated to all blocks."""
        blocks = self._parse_elixir(tmp_path)
        for block in blocks:
            assert block.file_path == "test.ex"

    def test_language_set_on_blocks(self, tmp_path: Path) -> None:
        """All blocks have language set to 'elixir'."""
        blocks = self._parse_elixir(tmp_path)
        for block in blocks:
            assert block.language == "elixir"

    def test_standalone_def(self, tmp_path: Path) -> None:
        """A top-level def (outside defmodule) is split as a function."""
        source = """\
def greet(name) do
  "Hello, #{name}"
end
"""
        blocks = self._parse_elixir(tmp_path, source)
        assert len(blocks) == 1
        assert blocks[0].symbol_name == "greet"
        assert blocks[0].symbol_type == "function"

    def test_standalone_defp(self, tmp_path: Path) -> None:
        """A top-level defp is split as a private function."""
        source = """\
defp helper(x) do
  x + 1
end
"""
        blocks = self._parse_elixir(tmp_path, source)
        assert len(blocks) == 1
        assert blocks[0].symbol_name == "helper"
        assert blocks[0].symbol_type == "function"

    def test_standalone_defmacro(self, tmp_path: Path) -> None:
        """A top-level defmacro is split as a macro."""
        source = """\
defmacro my_macro(expr) do
  quote do
    unquote(expr)
  end
end
"""
        blocks = self._parse_elixir(tmp_path, source)
        assert len(blocks) == 1
        assert blocks[0].symbol_name == "my_macro"
        assert blocks[0].symbol_type == "macro"

    def test_non_structural_calls_in_top_level(self, tmp_path: Path) -> None:
        """Non-structural calls like use/import/require go into top-level blocks."""
        source = """\
use GenServer
import Enum
require Logger

defmodule MyApp do
end
"""
        blocks = self._parse_elixir(tmp_path, source)
        # Should have a top-level block for use/import/require, then defmodule
        mod_blocks = [b for b in blocks if b.symbol_type == "module"]
        top_blocks = [b for b in blocks if b.symbol_type == "module_top"]
        assert len(mod_blocks) == 1
        assert len(top_blocks) == 1
        assert "use GenServer" in top_blocks[0].text

    def test_empty_file_produces_no_blocks(self, tmp_path: Path) -> None:
        """An empty .ex file produces no blocks."""
        ex_file = tmp_path / "empty.ex"
        ex_file.write_text("")
        doc = parse_code_file(ex_file, "elixir", "empty.ex")
        assert doc is not None
        assert len(doc.blocks) == 0

    def test_exs_file_parses(self, tmp_path: Path) -> None:
        """An .exs file (Elixir script) parses correctly."""
        exs_file = tmp_path / "test_helper.exs"
        exs_file.write_text("ExUnit.start()\n")
        doc = parse_code_file(exs_file, "elixir", "test_helper.exs")
        assert doc is not None
        assert doc.language == "elixir"
        # Single non-structural call should become a module_top or file block
        assert len(doc.blocks) >= 1

    def test_defmodule_start_line(self, tmp_path: Path) -> None:
        """The defmodule block starts on the correct line."""
        blocks = self._parse_elixir(tmp_path)
        mod_block = blocks[0]
        assert mod_block.start_line == 1

    def test_defmodule_end_line(self, tmp_path: Path) -> None:
        """The defmodule block ends on the correct line (the 'end' keyword)."""
        blocks = self._parse_elixir(tmp_path)
        mod_block = blocks[0]
        # defmodule MyApp.User starts at line 1, ends at line 19 (the 'end')
        assert mod_block.end_line == 19
