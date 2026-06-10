#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="${AI_CODE_INDEX_BIN:-ai-code-index}"
exec "$BIN" struct-search --root "$ROOT" "$@"
