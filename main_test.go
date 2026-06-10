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
