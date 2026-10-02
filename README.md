# ai-code-index

Local code index bootstrapper for AI coding agents.

`ai-code-index` creates a repository-local `.ai-code-index/` helper directory and wraps local search tools for consistent agent workflows:

- Zoekt for indexed text/code search.
- ast-grep for structural search.
- Universal Ctags for symbol search.
- ripgrep fallback when the Zoekt index is absent or insufficient.

No remote vector database, remote memory service, GraphRAG service, or paid indexing service is used.

## Install

```bash
brew install iwen-conf/tap/ai-code-index
```

## Quick Start

From a repository root:

```bash
ai-code-index init --reindex
```

Then use the generated helpers:

```bash
.ai-code-index/search.sh "query"
.ai-code-index/struct-search.sh go 'if err != nil { $$$ }'
.ai-code-index/symbols.sh "SymbolName"
.ai-code-index/reindex.sh
```

Run `.ai-code-index/reindex.sh` after every code, config, or documentation change so future searches use a fresh local index.

## Commands

```bash
ai-code-index init [--root DIR] [--force] [--reindex]
ai-code-index reindex [--root DIR]
ai-code-index search [--root DIR] "query" [paths...]
ai-code-index struct-search [--root DIR] <language> '<pattern>' [paths...]
ai-code-index symbols [--root DIR] [query]
ai-code-index doctor [--root DIR]
ai-code-index install-agent-rules [--home DIR] [--dry-run]
```


## Machine Protocol v1

KAG and other coding agents can use the stable JSON protocol instead of parsing the human CLI:

```bash
ai-code-index capabilities --json
ai-code-index search --format json --max 50 --context 2 "query"
ai-code-index symbol --format json --exact "SymbolName"
ai-code-index files --format json "auth"
ai-code-index ast --format json --lang rust 'fn $NAME()'
ai-code-index stats --format json
```

`capabilities --json` reports `name: "ai-code-index"` and `protocol_version: 1`. Clients should complete that handshake before exposing agent-facing search tools.

## Agent Rules

Install or update global Codex, Claude, and Gemini rules:

```bash
ai-code-index install-agent-rules
```

The command updates these files with an idempotent marked block:

- `~/.codex/AGENTS.md`
- `~/.claude/CLAUDE.md`
- `~/.gemini/GEMINI.md`

## Development

```bash
go test ./...
go build -o ai-code-index .
```
