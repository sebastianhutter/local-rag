#!/usr/bin/env bash
#
# build-dmg.sh — Create a DMG installer for local-rag.
#
# Expects the .app bundle to already exist at bin/local-rag.app
# (run `make app` first). Produces bin/local-rag.dmg
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
BUILD_DIR="$PROJECT_DIR/bin"
APP_DIR="$BUILD_DIR/local-rag.app"
DMG_PATH="$BUILD_DIR/local-rag.dmg"
VOLUME_NAME="Local RAG"

# ── Verify .app bundle exists ───────────────────────────────────────────────
if [[ ! -d "$APP_DIR" ]]; then
    echo "Error: .app bundle not found at $APP_DIR — run 'make app' first." >&2
    exit 1
fi

# ── Clean previous DMG ──────────────────────────────────────────────────────
rm -f "$DMG_PATH"

# ── Create staging directory ────────────────────────────────────────────────
STAGING=$(mktemp -d)
trap 'rm -rf "$STAGING"' EXIT

cp -R "$APP_DIR" "$STAGING/"
ln -s /Applications "$STAGING/Applications"

# ── Create DMG ──────────────────────────────────────────────────────────────
hdiutil create \
    -volname "$VOLUME_NAME" \
    -srcfolder "$STAGING" \
    -ov \
    -format UDZO \
    "$DMG_PATH"

echo "Created $DMG_PATH"
