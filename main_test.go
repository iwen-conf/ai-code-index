package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindRepoRootPrefersGit(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := findRepoRoot(nested)
	if err != nil {
		t.Fatal(err)
	}
	if got != root {
		t.Fatalf("got %q, want %q", got, root)
	}
}

func TestWriteHelperFilesPreservesExistingWithoutForce(t *testing.T) {
	root := t.TempDir()
	customSearch := filepath.Join(root, ".ai-code-index", "search.sh")
	if err := os.MkdirAll(filepath.Dir(customSearch), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(customSearch, []byte("#!/usr/bin/env bash\necho custom\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := writeHelperFiles(root, false); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(customSearch)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "echo custom") {
		t.Fatalf("existing helper was overwritten: %s", content)
	}
}

func TestUpsertMarkedBlockIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	if err := os.WriteFile(path, []byte("# Rules\n\nexisting\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	block := agentRulesBlock()
	if err := upsertMarkedBlock(path, block); err != nil {
		t.Fatal(err)
	}
	if err := upsertMarkedBlock(path, block); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	if strings.Count(text, "<!-- ai-code-index:start -->") != 1 {
		t.Fatalf("expected one start marker, got:\n%s", text)
	}
	if !strings.Contains(text, "existing") {
		t.Fatalf("existing content was not preserved:\n%s", text)
	}
}

func TestProtocolV1Capabilities(t *testing.T) {
	response := capabilitiesResponse{
		Name:            "ai-code-index",
		ProtocolVersion: protocolVersion,
		Version:         version,
		Capabilities: capabilityFlags{
			Search: true,
			Symbol: true,
			Files:  true,
			AST:    true,
			Stats:  true,
		},
		Formats: []string{"json"},
	}
	if response.Name != "ai-code-index" {
		t.Fatalf("unexpected protocol name %q", response.Name)
	}
	if response.ProtocolVersion != 1 {
		t.Fatalf("unexpected protocol version %d", response.ProtocolVersion)
	}
	if !response.Capabilities.Search || !response.Capabilities.Symbol ||
		!response.Capabilities.Files || !response.Capabilities.AST ||
		!response.Capabilities.Stats {
		t.Fatalf("required protocol capability missing: %+v", response.Capabilities)
	}
}

func TestMachineLimitAndKindAliases(t *testing.T) {
	lines, truncated := limitOutputLines("a\nb\nc\n", 2)
	if !truncated || len(lines) != 2 || lines[0] != "a" || lines[1] != "b" {
		t.Fatalf("unexpected line limit result: %#v truncated=%v", lines, truncated)
	}
	if !kindMatches("function", "f") {
		t.Fatal("function should match ctags kind f")
	}
	if kindMatches("struct", "f") {
		t.Fatal("struct should not match ctags kind f")
	}
}
