#!/usr/bin/env bash
#
# build-app.sh — Create a macOS .app bundle for local-rag.
#
# Expects the binary to already be built at bin/local-rag (run `make build` first).
# Produces bin/local-rag.app/
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
BUILD_DIR="$PROJECT_DIR/bin"
BINARY="$BUILD_DIR/local-rag"
APP_DIR="$BUILD_DIR/local-rag.app"

BUNDLE_ID="com.sebastianhutter.local-rag"
APP_NAME="local-rag"
VERSION="${VERSION:-0.1.0}"

# ── Verify binary exists ────────────────────────────────────────────────────
if [[ ! -f "$BINARY" ]]; then
    echo "Error: binary not found at $BINARY — run 'make build' first." >&2
    exit 1
fi

# ── Verify Icon.png exists ──────────────────────────────────────────────────
ICON_SRC="$PROJECT_DIR/Icon.png"
if [[ ! -f "$ICON_SRC" ]]; then
    echo "Error: Icon.png not found at $ICON_SRC" >&2
    exit 1
fi

# ── Clean previous bundle ───────────────────────────────────────────────────
rm -rf "$APP_DIR"

# ── Create .app directory structure ─────────────────────────────────────────
mkdir -p "$APP_DIR/Contents/MacOS"
mkdir -p "$APP_DIR/Contents/Resources"

# ── Copy binary ─────────────────────────────────────────────────────────────
cp "$BINARY" "$APP_DIR/Contents/MacOS/$APP_NAME"
chmod +x "$APP_DIR/Contents/MacOS/$APP_NAME"

# ── Convert Icon.png → .icns ────────────────────────────────────────────────
ICONSET_DIR=$(mktemp -d)/local-rag.iconset
mkdir -p "$ICONSET_DIR"

# Generate all required icon sizes from the 512×512 source.
sips -z 16 16     "$ICON_SRC" --out "$ICONSET_DIR/icon_16x16.png"      >/dev/null
sips -z 32 32     "$ICON_SRC" --out "$ICONSET_DIR/icon_16x16@2x.png"   >/dev/null
sips -z 32 32     "$ICON_SRC" --out "$ICONSET_DIR/icon_32x32.png"      >/dev/null
sips -z 64 64     "$ICON_SRC" --out "$ICONSET_DIR/icon_32x32@2x.png"   >/dev/null
sips -z 128 128   "$ICON_SRC" --out "$ICONSET_DIR/icon_128x128.png"    >/dev/null
sips -z 256 256   "$ICON_SRC" --out "$ICONSET_DIR/icon_128x128@2x.png" >/dev/null
sips -z 256 256   "$ICON_SRC" --out "$ICONSET_DIR/icon_256x256.png"    >/dev/null
sips -z 512 512   "$ICON_SRC" --out "$ICONSET_DIR/icon_256x256@2x.png" >/dev/null
sips -z 512 512   "$ICON_SRC" --out "$ICONSET_DIR/icon_512x512.png"    >/dev/null
# For 512@2x we'd need 1024, but our source is 512 — just duplicate.
cp "$ICON_SRC" "$ICONSET_DIR/icon_512x512@2x.png"

iconutil -c icns "$ICONSET_DIR" -o "$APP_DIR/Contents/Resources/$APP_NAME.icns"
rm -rf "$(dirname "$ICONSET_DIR")"

# ── Generate Info.plist ─────────────────────────────────────────────────────
cat > "$APP_DIR/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleName</key>
    <string>${APP_NAME}</string>
    <key>CFBundleDisplayName</key>
    <string>Local RAG</string>
    <key>CFBundleIdentifier</key>
    <string>${BUNDLE_ID}</string>
    <key>CFBundleVersion</key>
    <string>${VERSION}</string>
    <key>CFBundleShortVersionString</key>
    <string>${VERSION}</string>
    <key>CFBundleExecutable</key>
    <string>${APP_NAME}</string>
    <key>CFBundleIconFile</key>
    <string>${APP_NAME}</string>
    <key>CFBundlePackageType</key>
    <string>APPL</string>
    <key>LSUIElement</key>
    <true/>
    <key>NSHighResolutionCapable</key>
    <true/>
    <key>LSMinimumSystemVersion</key>
    <string>12.0</string>
</dict>
</plist>
PLIST

echo "Created $APP_DIR"
