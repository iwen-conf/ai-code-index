package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const freshnessStateVersion = 1

type freshnessState struct {
	Version           int    `json:"version"`
	TextFingerprint   string `json:"text_fingerprint,omitempty"`
	SymbolFingerprint string `json:"symbol_fingerprint,omitempty"`
}

func freshnessPath(root string) string {
	return filepath.Join(root, ".ai-code-index", "freshness.json")
}

func repositoryFingerprint(root string) (string, error) {
	files, err := listRepoFiles(root)
	if err != nil {
		return "", err
	}

	hasher := sha256.New()
	for _, rel := range files {
		rel = filepath.ToSlash(rel)
		if ignoreFreshnessPath(rel) {
			continue
		}
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			if os.IsNotExist(err) {
				_, _ = fmt.Fprintf(hasher, "missing\x00%s\x00", rel)
				continue
			}
			return "", err
		}
		_, _ = fmt.Fprintf(
			hasher,
			"%s\x00%d\x00%d\x00%d\x00",
			rel,
			info.Size(),
			info.ModTime().UnixNano(),
			uint32(info.Mode()),
		)
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func ignoreFreshnessPath(rel string) bool {
	rel = filepath.ToSlash(rel)
	if rel == ".ai-code-index/freshness.json" ||
		rel == ".ai-code-index/repo.meta.json" ||
		rel == ".ai-code-index/tags" {
		return true
	}
	return strings.HasPrefix(rel, ".ai-code-index/index/")
}

func loadFreshness(root string) freshnessState {
	bytes, err := os.ReadFile(freshnessPath(root))
	if err != nil {
		return freshnessState{Version: freshnessStateVersion}
	}
	var state freshnessState
	if json.Unmarshal(bytes, &state) != nil || state.Version != freshnessStateVersion {
		return freshnessState{Version: freshnessStateVersion}
	}
	return state
}

func saveFreshness(root string, state freshnessState) error {
	state.Version = freshnessStateVersion
	path := freshnessPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	bytes, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	bytes = append(bytes, '\n')
	return os.WriteFile(path, bytes, 0o644)
}

func markFresh(root string, textIndex bool, symbolIndex bool) error {
	fingerprint, err := repositoryFingerprint(root)
	if err != nil {
		return err
	}
	state := loadFreshness(root)
	if textIndex {
		state.TextFingerprint = fingerprint
	}
	if symbolIndex {
		state.SymbolFingerprint = fingerprint
	}
	return saveFreshness(root, state)
}

func textIndexFresh(root string) bool {
	state := loadFreshness(root)
	if state.TextFingerprint == "" {
		return false
	}
	fingerprint, err := repositoryFingerprint(root)
	return err == nil && fingerprint == state.TextFingerprint
}

func symbolIndexFresh(root string) bool {
	state := loadFreshness(root)
	if state.SymbolFingerprint == "" {
		return false
	}
	fingerprint, err := repositoryFingerprint(root)
	return err == nil && fingerprint == state.SymbolFingerprint
}
