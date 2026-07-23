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
# Size the intermediate image to the staged content plus generous headroom.
# hdiutil's automatic sizing can under-provision the scratch volume and fail
# with "No space left on device" while copying the app in. The final UDZO image
# is compressed, so over-allocating the read/write intermediate costs nothing.
STAGING_MB=$(du -sm "$STAGING" | cut -f1)
SIZE_MB=$(( STAGING_MB + 100 ))

hdiutil create \
    -volname "$VOLUME_NAME" \
    -srcfolder "$STAGING" \
    -size "${SIZE_MB}m" \
    -ov \
    -format UDZO \
    "$DMG_PATH"

echo "Created $DMG_PATH"
