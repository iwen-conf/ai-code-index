package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const protocolVersion = 1
const defaultMachineLimit = 50
const maxMachineLimit = 1000

type capabilityFlags struct {
	Search bool `json:"search"`
	Symbol bool `json:"symbol"`
	Files  bool `json:"files"`
	AST    bool `json:"ast"`
	Stats  bool `json:"stats"`
}

type capabilitiesResponse struct {
	Name            string          `json:"name"`
	ProtocolVersion int             `json:"protocol_version"`
	Version         string          `json:"version"`
	Capabilities    capabilityFlags `json:"capabilities"`
	Formats         []string        `json:"formats"`
}

type machineLineResponse struct {
	ProtocolVersion int      `json:"protocol_version"`
	Mode            string   `json:"mode"`
	Backend         string   `json:"backend,omitempty"`
	Results         []string `json:"results"`
	Truncated       bool     `json:"truncated"`
}

type machineSymbolResult struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Address string `json:"address,omitempty"`
	Kind    string `json:"kind,omitempty"`
}

type machineSymbolResponse struct {
	ProtocolVersion int                   `json:"protocol_version"`
	Mode            string                `json:"mode"`
	Results         []machineSymbolResult `json:"results"`
	Truncated       bool                  `json:"truncated"`
}

type machineFilesResponse struct {
	ProtocolVersion int      `json:"protocol_version"`
	Mode            string   `json:"mode"`
	Results         []string `json:"results"`
	Truncated       bool     `json:"truncated"`
}

type machineStatsResponse struct {
	ProtocolVersion  int             `json:"protocol_version"`
	Mode             string          `json:"mode"`
	Root             string          `json:"root"`
	FileCount        int             `json:"file_count"`
	SymbolCount      int             `json:"symbol_count"`
	ZoektShards      int             `json:"zoekt_shards"`
	IndexPresent     bool            `json:"index_present"`
	TextIndexFresh   bool            `json:"text_index_fresh"`
	TagsPresent      bool            `json:"tags_present"`
	SymbolIndexFresh bool            `json:"symbol_index_fresh"`
	Tools            map[string]bool `json:"tools"`
}

func cmdCapabilities(args []string) error {
	fs := flag.NewFlagSet("capabilities", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonFlag := fs.Bool("json", false, "emit machine-readable JSON")
	formatFlag := fs.String("format", "", "output format")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("capabilities does not accept positional arguments")
	}
	if *formatFlag != "" && *formatFlag != "json" {
		return fmt.Errorf("unsupported capabilities format %q; expected json", *formatFlag)
	}

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
	if *jsonFlag || *formatFlag == "json" {
		return json.NewEncoder(os.Stdout).Encode(response)
	}
	fmt.Printf(
		"ai-code-index protocol v%d (search, symbol, files, ast, stats)\n",
		protocolVersion,
	)
	return nil
}

func runMachineSearch(root string, queryArgs []string, maxResults int, contextLines int) error {
	limit := normalizeMachineLimit(maxResults)
	if contextLines < 0 {
		contextLines = 0
	}
	if contextLines > 20 {
		contextLines = 20
	}

	indexDir := filepath.Join(root, ".ai-code-index", "index")
	if hasZoektIndex(indexDir) && textIndexFresh(root) {
		if zoekt, ok := lookPath("zoekt"); ok {
			stdout, stderr, code, err := runCaptured(
				zoekt,
				append([]string{"-index_dir", indexDir}, queryArgs...),
				root,
			)
			if err != nil {
				return err
			}
			if code > 1 {
				return commandFailure("zoekt", code, stderr)
			}
			results, truncated := limitOutputLines(stdout, limit)
			return json.NewEncoder(os.Stdout).Encode(machineLineResponse{
				ProtocolVersion: protocolVersion,
				Mode:            "search",
				Backend:         "zoekt",
				Results:         results,
				Truncated:       truncated,
			})
		}
	}

	rg, ok := lookPath("rg")
	if !ok {
		return errors.New("neither zoekt nor rg is available")
	}
	rgArgs := []string{
		"--hidden",
		"--line-number",
		"--column",
		"--no-heading",
		"--color=never",
	}
	if contextLines > 0 {
		rgArgs = append(rgArgs, "-C", fmt.Sprintf("%d", contextLines))
	}
	for _, dir := range ignoredDirs {
		rgArgs = append(rgArgs, "--glob", "!"+filepath.ToSlash(dir)+"/**")
	}
	rgArgs = append(rgArgs, queryArgs...)
	stdout, stderr, code, err := runCaptured(rg, rgArgs, root)
	if err != nil {
		return err
	}
	if code > 1 {
		return commandFailure("rg", code, stderr)
	}
	results, truncated := limitOutputLines(stdout, limit)
	return json.NewEncoder(os.Stdout).Encode(machineLineResponse{
		ProtocolVersion: protocolVersion,
		Mode:            "search",
		Backend:         "rg",
		Results:         results,
		Truncated:       truncated,
	})
}

func cmdSymbol(args []string) error {
	fs := flag.NewFlagSet("symbol", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	rootFlag := fs.String("root", "", "repository root")
	formatFlag := fs.String("format", "", "output format")
	kindFlag := fs.String("kind", "", "ctags kind")
	exactFlag := fs.Bool("exact", false, "require an exact symbol name")
	maxFlag := fs.Int("max", defaultMachineLimit, "maximum results")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *formatFlag != "json" {
		return errors.New("symbol machine command requires --format json")
	}
	query := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if query == "" {
		return errors.New("symbol requires a query")
	}
	root, err := resolveRoot(*rootFlag)
	if err != nil {
		return err
	}
	if !symbolIndexFresh(root) {
		refreshed, refreshErr := rebuildTags(root)
		if refreshErr != nil {
			return fmt.Errorf("symbol index is stale and could not be refreshed: %w", refreshErr)
		}
		if !refreshed {
			return errors.New("symbol index is stale and ctags is not available")
		}
		if err := markFresh(root, false, true); err != nil {
			return fmt.Errorf("symbol index refreshed but freshness metadata could not be updated: %w", err)
		}
	}

	tagsFile := filepath.Join(root, ".ai-code-index", "tags")
	file, err := os.Open(tagsFile)
	if err != nil {
		return errors.New("no ctags symbol file found; run ai-code-index reindex first")
	}
	defer file.Close()

	limit := normalizeMachineLimit(*maxFlag)
	results := make([]machineSymbolResult, 0, limit)
	truncated := false
	queryLower := strings.ToLower(query)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "!") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 3 {
			continue
		}
		name := parts[0]
		if *exactFlag {
			if name != query {
				continue
			}
		} else if !strings.Contains(strings.ToLower(name), queryLower) {
			continue
		}
		kind := ""
		if len(parts) >= 4 {
			kind = strings.TrimSpace(parts[3])
		}
		if !kindMatches(*kindFlag, kind) {
			continue
		}
		if len(results) >= limit {
			truncated = true
			break
		}
		results = append(results, machineSymbolResult{
			Name:    name,
			Path:    parts[1],
			Address: strings.TrimSuffix(parts[2], ";\""),
			Kind:    kind,
		})
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	return json.NewEncoder(os.Stdout).Encode(machineSymbolResponse{
		ProtocolVersion: protocolVersion,
		Mode:            "symbol",
		Results:         results,
		Truncated:       truncated,
	})
}

func cmdFiles(args []string) error {
	fs := flag.NewFlagSet("files", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	rootFlag := fs.String("root", "", "repository root")
	formatFlag := fs.String("format", "", "output format")
	maxFlag := fs.Int("max", 500, "maximum results")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *formatFlag != "json" {
		return errors.New("files machine command requires --format json")
	}
	root, err := resolveRoot(*rootFlag)
	if err != nil {
		return err
	}
	files, err := listRepoFiles(root)
	if err != nil {
		return err
	}
	query := strings.ToLower(strings.TrimSpace(strings.Join(fs.Args(), " ")))
	limit := normalizeMachineLimit(*maxFlag)
	results := make([]string, 0, minInt(limit, len(files)))
	truncated := false
	for _, path := range files {
		if query != "" && !strings.Contains(strings.ToLower(path), query) {
			continue
		}
		if len(results) >= limit {
			truncated = true
			break
		}
		results = append(results, path)
	}
	return json.NewEncoder(os.Stdout).Encode(machineFilesResponse{
		ProtocolVersion: protocolVersion,
		Mode:            "files",
		Results:         results,
		Truncated:       truncated,
	})
}

func cmdAST(args []string) error {
	fs := flag.NewFlagSet("ast", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	rootFlag := fs.String("root", "", "repository root")
	formatFlag := fs.String("format", "", "output format")
	languageFlag := fs.String("lang", "", "ast-grep language")
	maxFlag := fs.Int("max", defaultMachineLimit, "maximum output lines")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *formatFlag != "json" {
		return errors.New("ast machine command requires --format json")
	}
	language := strings.TrimSpace(*languageFlag)
	if language == "" {
		return errors.New("ast requires --lang")
	}
	pattern := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if pattern == "" {
		return errors.New("ast requires a pattern")
	}
	root, err := resolveRoot(*rootFlag)
	if err != nil {
		return err
	}
	bin, ok := lookPath("sg")
	if !ok {
		bin, ok = lookPath("ast-grep")
	}
	if !ok {
		return errors.New("ast-grep/sg is not available")
	}

	sgArgs := []string{"run", "--lang", language, "--pattern", pattern}
	for _, dir := range ignoredDirs {
		sgArgs = append(sgArgs, "--globs", "!"+filepath.ToSlash(dir)+"/**")
	}
	sgArgs = append(sgArgs, root)
	stdout, stderr, code, err := runCaptured(bin, sgArgs, root)
	if err != nil {
		return err
	}
	if code > 1 {
		return commandFailure("ast-grep", code, stderr)
	}
	results, truncated := limitOutputLines(stdout, normalizeMachineLimit(*maxFlag))
	return json.NewEncoder(os.Stdout).Encode(machineLineResponse{
		ProtocolVersion: protocolVersion,
		Mode:            "ast",
		Backend:         "ast-grep",
		Results:         results,
		Truncated:       truncated,
	})
}

func cmdStats(args []string) error {
	fs := flag.NewFlagSet("stats", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	rootFlag := fs.String("root", "", "repository root")
	formatFlag := fs.String("format", "", "output format")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *formatFlag != "json" {
		return errors.New("stats machine command requires --format json")
	}
	if fs.NArg() != 0 {
		return errors.New("stats does not accept positional arguments")
	}
	root, err := resolveRoot(*rootFlag)
	if err != nil {
		return err
	}
	files, err := listRepoFiles(root)
	if err != nil {
		return err
	}
	tagsFile := filepath.Join(root, ".ai-code-index", "tags")
	symbolCount, tagsPresent := countTagEntries(tagsFile)
	indexDir := filepath.Join(root, ".ai-code-index", "index")
	shards, _ := filepath.Glob(filepath.Join(indexDir, "*.zoekt"))

	tools := make(map[string]bool, 6)
	for _, name := range []string{"zoekt-index", "zoekt", "sg", "ast-grep", "rg", "ctags"} {
		_, tools[name] = lookPath(name)
	}
	if ctags, ok := lookCtags(); ok {
		tools["ctags"] = ctags != ""
	}

	return json.NewEncoder(os.Stdout).Encode(machineStatsResponse{
		ProtocolVersion:  protocolVersion,
		Mode:             "stats",
		Root:             root,
		FileCount:        len(files),
		SymbolCount:      symbolCount,
		ZoektShards:      len(shards),
		IndexPresent:     len(shards) > 0,
		TextIndexFresh:   len(shards) > 0 && textIndexFresh(root),
		TagsPresent:      tagsPresent,
		SymbolIndexFresh: tagsPresent && symbolIndexFresh(root),
		Tools:            tools,
	})
}

func normalizeMachineLimit(limit int) int {
	if limit <= 0 {
		return defaultMachineLimit
	}
	if limit > maxMachineLimit {
		return maxMachineLimit
	}
	return limit
}

func limitOutputLines(output string, limit int) ([]string, bool) {
	trimmed := strings.TrimRight(output, "\r\n")
	if trimmed == "" {
		return []string{}, false
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) <= limit {
		return lines, false
	}
	return lines[:limit], true
}

func runCaptured(bin string, args []string, dir string) (string, string, int, error) {
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Stdin = nil
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.String(), stderr.String(), 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return stdout.String(), stderr.String(), exitErr.ExitCode(), nil
	}
	return stdout.String(), stderr.String(), -1, err
}

func commandFailure(command string, code int, stderr string) error {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return fmt.Errorf("%s exited with status %d", command, code)
	}
	return fmt.Errorf("%s exited with status %d: %s", command, code, stderr)
}

func listRepoFiles(root string) ([]string, error) {
	if git, ok := lookPath("git"); ok {
		stdout, _, code, err := runCaptured(
			git,
			[]string{"-C", root, "ls-files", "--cached", "--others", "--exclude-standard"},
			root,
		)
		if err == nil && code == 0 {
			files, _ := limitOutputLines(stdout, maxInt)
			sort.Strings(files)
			return files, nil
		}
	}

	files := make([]string, 0, 256)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if shouldIgnoreRelativePath(rel) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.IsDir() {
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

const maxInt = int(^uint(0) >> 1)

func shouldIgnoreRelativePath(rel string) bool {
	rel = filepath.ToSlash(rel)
	parts := strings.Split(rel, "/")
	for _, ignored := range ignoredDirs {
		ignored = filepath.ToSlash(ignored)
		if strings.Contains(ignored, "/") {
			if rel == ignored || strings.HasPrefix(rel, ignored+"/") {
				return true
			}
			continue
		}
		for _, part := range parts {
			if part == ignored {
				return true
			}
		}
	}
	return false
}

func countTagEntries(path string) (int, bool) {
	file, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer file.Close()
	count := 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if !strings.HasPrefix(scanner.Text(), "!") {
			count++
		}
	}
	return count, count > 0
}

func kindMatches(requested string, actual string) bool {
	requested = strings.ToLower(strings.TrimSpace(requested))
	if requested == "" {
		return true
	}
	actual = strings.ToLower(strings.TrimSpace(actual))
	if requested == actual {
		return true
	}
	aliases := map[string]string{
		"func":      "f",
		"function":  "f",
		"method":    "m",
		"struct":    "s",
		"class":     "c",
		"interface": "i",
		"variable":  "v",
		"var":       "v",
		"constant":  "c",
	}
	return aliases[requested] != "" && aliases[requested] == actual
}

func minInt(left int, right int) int {
	if left < right {
		return left
	}
	return right
}
