package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var version = "0.2.0"

var ignoredDirs = []string{
	".git",
	".hg",
	".svn",
	"node_modules",
	"dist",
	"build",
	"target",
	".next",
	".nuxt",
	"coverage",
	"tmp",
	".tmp",
	"vendor",
	".venv",
	"__pycache__",
	".cache",
	".tool",
	".dart_tool",
	".gradle",
	".terraform",
	".serverless",
	".pnpm-store",
	"miniprogram_npm",
	".plugin_symlinks",
	"ephemeral",
	"frontend-ota",
	".ai-code-index/index",
}

func main() {
	if len(os.Args) < 2 {
		usage(os.Stdout)
		return
	}

	var err error
	switch os.Args[1] {
	case "init":
		err = cmdInit(os.Args[2:])
	case "reindex":
		err = cmdReindex(os.Args[2:])
	case "capabilities":
		err = cmdCapabilities(os.Args[2:])
	case "search":
		err = cmdSearch(os.Args[2:])
	case "symbol":
		err = cmdSymbol(os.Args[2:])
	case "files":
		err = cmdFiles(os.Args[2:])
	case "ast":
		err = cmdAST(os.Args[2:])
	case "stats":
		err = cmdStats(os.Args[2:])
	case "struct-search":
		err = cmdStructSearch(os.Args[2:])
	case "symbols":
		err = cmdSymbols(os.Args[2:])
	case "doctor":
		err = cmdDoctor(os.Args[2:])
	case "install-agent-rules":
		err = cmdInstallAgentRules(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println(version)
	case "help", "--help", "-h":
		usage(os.Stdout)
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ai-code-index:", err)
		os.Exit(1)
	}
}

func usage(w io.Writer) {
	fmt.Fprintf(w, `ai-code-index %s

Local repository indexing helper for AI coding agents.

Usage:
  ai-code-index init [--root DIR] [--force] [--reindex]
  ai-code-index reindex [--root DIR]
  ai-code-index capabilities [--json]
  ai-code-index search [--root DIR] [--format json] [--max N] [--context N] "query" [paths...]
  ai-code-index symbol --format json [--root DIR] [--kind KIND] [--exact] "query"
  ai-code-index files --format json [--root DIR] [--max N] [query]
  ai-code-index ast --format json [--root DIR] --lang LANGUAGE [--max N] '<pattern>'
  ai-code-index stats --format json [--root DIR]
  ai-code-index struct-search [--root DIR] <language> '<pattern>' [paths...]
  ai-code-index symbols [--root DIR] [query]
  ai-code-index doctor [--root DIR]
  ai-code-index install-agent-rules [--home DIR] [--dry-run]

`, version)
}

func cmdInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	rootFlag := fs.String("root", "", "repository root")
	force := fs.Bool("force", false, "overwrite existing helper files")
	reindex := fs.Bool("reindex", false, "run reindex after creating helpers")
	_ = fs.Parse(args)

	root, err := resolveRoot(*rootFlag)
	if err != nil {
		return err
	}
	if err := writeHelperFiles(root, *force); err != nil {
		return err
	}
	if *reindex {
		return runReindex(root)
	}
	return nil
}

func cmdReindex(args []string) error {
	fs := flag.NewFlagSet("reindex", flag.ExitOnError)
	rootFlag := fs.String("root", "", "repository root")
	_ = fs.Parse(args)

	root, err := resolveRoot(*rootFlag)
	if err != nil {
		return err
	}
	return runReindex(root)
}

func cmdSearch(args []string) error {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	rootFlag := fs.String("root", "", "repository root")
	formatFlag := fs.String("format", "", "output format (json for machine protocol)")
	maxFlag := fs.Int("max", defaultMachineLimit, "maximum machine-protocol results")
	contextFlag := fs.Int("context", 0, "context lines for machine-protocol search")
	if err := fs.Parse(args); err != nil {
		return err
	}
	queryArgs := fs.Args()
	if len(queryArgs) == 0 {
		return errors.New(`usage: ai-code-index search "query" [paths...]`)
	}

	root, err := resolveRoot(*rootFlag)
	if err != nil {
		return err
	}
	if *formatFlag != "" {
		if *formatFlag != "json" {
			return fmt.Errorf("unsupported search format %q; expected json", *formatFlag)
		}
		return runMachineSearch(root, queryArgs, *maxFlag, *contextFlag)
	}

	indexDir := filepath.Join(root, ".ai-code-index", "index")
	if hasZoektIndex(indexDir) && textIndexFresh(root) {
		if zoekt, ok := lookPath("zoekt"); ok {
			return run(zoekt, append([]string{"-index_dir", indexDir}, queryArgs...), root)
		}
		fmt.Fprintln(os.Stderr, "zoekt search binary not found; falling back to live rg")
	} else if hasZoektIndex(indexDir) {
		fmt.Fprintln(os.Stderr, "Zoekt index is stale; falling back to live rg")
	} else {
		fmt.Fprintln(os.Stderr, "no Zoekt index found; falling back to live rg")
	}

	rg, ok := lookPath("rg")
	if !ok {
		return errors.New("neither zoekt nor rg is available")
	}
	rgArgs := []string{"--hidden"}
	for _, dir := range ignoredDirs {
		rgArgs = append(rgArgs, "--glob", "!"+dir)
	}
	rgArgs = append(rgArgs, queryArgs...)
	return run(rg, rgArgs, root)
}

func cmdStructSearch(args []string) error {
	fs := flag.NewFlagSet("struct-search", flag.ExitOnError)
	rootFlag := fs.String("root", "", "repository root")
	_ = fs.Parse(args)
	rest := fs.Args()
	if len(rest) < 2 {
		return errors.New("usage: ai-code-index struct-search <language> '<pattern>' [paths...]")
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

	language := rest[0]
	pattern := rest[1]
	paths := rest[2:]
	if len(paths) == 0 {
		paths = []string{root}
	}

	sgArgs := []string{"run", "--lang", language, "--pattern", pattern}
	for _, dir := range ignoredDirs {
		sgArgs = append(sgArgs, "--globs", "!"+dir)
	}
	sgArgs = append(sgArgs, paths...)
	return run(bin, sgArgs, root)
}

func cmdSymbols(args []string) error {
	fs := flag.NewFlagSet("symbols", flag.ExitOnError)
	rootFlag := fs.String("root", "", "repository root")
	_ = fs.Parse(args)
	query := strings.ToLower(strings.Join(fs.Args(), " "))

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
		fmt.Fprintln(os.Stderr, "symbol index was stale; refreshed ctags before searching")
	}
	tagsFile := filepath.Join(root, ".ai-code-index", "tags")
	f, err := os.Open(tagsFile)
	if err != nil {
		return errors.New("no ctags symbol file found; run .ai-code-index/reindex.sh first")
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	count := 0
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "!") {
			continue
		}
		if query == "" || strings.Contains(strings.ToLower(line), query) {
			parts := strings.Split(line, "\t")
			if len(parts) >= 3 {
				fmt.Printf("%s\t%s\t%s\n", parts[0], parts[1], parts[2])
			} else {
				fmt.Println(line)
			}
			count++
			if count >= 200 {
				break
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

func cmdDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	rootFlag := fs.String("root", "", "repository root")
	_ = fs.Parse(args)

	root, err := resolveRoot(*rootFlag)
	if err != nil {
		return err
	}

	fmt.Println("root:", root)
	for _, name := range []string{"zoekt-index", "zoekt", "sg", "ast-grep", "rg"} {
		if p, ok := lookPath(name); ok {
			fmt.Printf("[OK] %s: %s\n", name, p)
		} else {
			fmt.Printf("[WARN] %s: not found\n", name)
		}
	}
	if p, ok := lookCtags(); ok {
		fmt.Printf("[OK] ctags: %s\n", p)
	} else {
		fmt.Println("[WARN] ctags: Universal/Exuberant ctags not found")
	}
	for _, rel := range []string{
		".ai-code-index/reindex.sh",
		".ai-code-index/search.sh",
		".ai-code-index/struct-search.sh",
		".ai-code-index/symbols.sh",
		".ai-code-index/README.md",
	} {
		if info, err := os.Stat(filepath.Join(root, rel)); err == nil && !info.IsDir() {
			fmt.Printf("[OK] %s\n", rel)
		} else {
			fmt.Printf("[WARN] %s missing\n", rel)
		}
	}
	indexDir := filepath.Join(root, ".ai-code-index", "index")
	if hasZoektIndex(indexDir) {
		if textIndexFresh(root) {
			fmt.Printf("[OK] Zoekt index fresh: %s\n", indexDir)
		} else {
			fmt.Printf("[WARN] Zoekt index stale: %s (search will use live rg)\n", indexDir)
		}
	} else {
		fmt.Printf("[WARN] Zoekt index missing: %s\n", indexDir)
	}
	if info, err := os.Stat(filepath.Join(root, ".ai-code-index", "tags")); err == nil && info.Size() > 0 {
		if symbolIndexFresh(root) {
			fmt.Println("[OK] ctags symbols fresh:", filepath.Join(root, ".ai-code-index", "tags"))
		} else {
			fmt.Println("[WARN] ctags symbols stale; next symbol lookup will refresh them")
		}
	} else {
		fmt.Println("[WARN] ctags symbols missing")
	}
	return nil
}

func cmdInstallAgentRules(args []string) error {
	fs := flag.NewFlagSet("install-agent-rules", flag.ExitOnError)
	homeFlag := fs.String("home", "", "home directory containing .codex/.claude/.gemini")
	dryRun := fs.Bool("dry-run", false, "print target files without writing")
	_ = fs.Parse(args)

	home := *homeFlag
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return err
		}
	}
	targets := []string{
		filepath.Join(home, ".codex", "AGENTS.md"),
		filepath.Join(home, ".claude", "CLAUDE.md"),
		filepath.Join(home, ".gemini", "GEMINI.md"),
	}
	for _, target := range targets {
		if *dryRun {
			fmt.Println("would update", target)
			continue
		}
		if err := upsertMarkedBlock(target, agentRulesBlock()); err != nil {
			return err
		}
		fmt.Println("updated", target)
	}
	return nil
}

func writeHelperFiles(root string, force bool) error {
	dir := filepath.Join(root, ".ai-code-index")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	files := map[string]struct {
		mode    os.FileMode
		content string
	}{
		"reindex.sh": {
			mode: 0o755,
			content: `#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="${AI_CODE_INDEX_BIN:-ai-code-index}"
exec "$BIN" reindex --root "$ROOT" "$@"
`,
		},
		"search.sh": {
			mode: 0o755,
			content: `#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="${AI_CODE_INDEX_BIN:-ai-code-index}"
exec "$BIN" search --root "$ROOT" "$@"
`,
		},
		"struct-search.sh": {
			mode: 0o755,
			content: `#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="${AI_CODE_INDEX_BIN:-ai-code-index}"
exec "$BIN" struct-search --root "$ROOT" "$@"
`,
		},
		"symbols.sh": {
			mode: 0o755,
			content: `#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="${AI_CODE_INDEX_BIN:-ai-code-index}"
exec "$BIN" symbols --root "$ROOT" "$@"
`,
		},
		"README.md": {
			mode: 0o644,
			content: `# Local Code Index

This directory contains local-only repository search helpers generated by ` + "`ai-code-index`" + `.

## Commands

` + "```bash" + `
.ai-code-index/reindex.sh
.ai-code-index/search.sh "query"
.ai-code-index/struct-search.sh typescript 'console.log($$$)'
.ai-code-index/symbols.sh "SymbolName"
` + "```" + `

Search helpers detect stale indexes automatically: text search falls back to live ` + "`rg`" + `, and symbol search refreshes ctags before use. Run ` + "`.ai-code-index/reindex.sh`" + ` after edits when you want indexed Zoekt performance again.

Generated index files are ignored by Git. The helpers use local tools only and do not require sudo or remote services.
`,
		},
		".gitignore": {
			mode: 0o644,
			content: `/index/
/tags
/repo.meta.json
/freshness.json
`,
		},
	}

	for rel, f := range files {
		path := filepath.Join(dir, rel)
		if err := writeFile(path, []byte(f.content), f.mode, force); err != nil {
			return err
		}
	}
	return nil
}

func writeFile(path string, content []byte, mode os.FileMode, force bool) error {
	if current, err := os.ReadFile(path); err == nil {
		if bytes.Equal(current, content) {
			fmt.Println("unchanged", path)
			return os.Chmod(path, mode)
		}
		if !force {
			fmt.Println("kept existing", path)
			return os.Chmod(path, mode)
		}
	}
	if err := os.WriteFile(path, content, mode); err != nil {
		return err
	}
	fmt.Println("wrote", path)
	return nil
}

func runReindex(root string) error {
	dir := filepath.Join(root, ".ai-code-index")
	indexDir := filepath.Join(dir, "index")
	tagsFile := filepath.Join(dir, "tags")
	metaFile := filepath.Join(dir, "repo.meta.json")

	if err := os.MkdirAll(indexDir, 0o755); err != nil {
		return err
	}
	remoteURL := gitRemoteURL(root)
	meta := map[string]string{
		"Name": filepath.Base(root),
		"URL":  remoteURL,
	}
	metaBytes, _ := json.Marshal(meta)
	if err := os.WriteFile(metaFile, append(metaBytes, '\n'), 0o644); err != nil {
		return err
	}

	didWork := false
	textIndexed := false
	symbolIndexed := false
	if zoektIndex, ok := lookPath("zoekt-index"); ok {
		if err := removeZoektShards(indexDir); err != nil {
			return err
		}
		args := []string{
			"-index", indexDir,
			"-ignore_dirs", strings.Join(ignoredDirs, ","),
			"-meta", metaFile,
			"-shard_prefix_override", filepath.Base(root),
			root,
		}
		if err := run(zoektIndex, args, root); err != nil {
			return err
		}
		fmt.Println("Zoekt index:", indexDir)
		didWork = true
		textIndexed = true
	} else {
		fmt.Fprintln(os.Stderr, "zoekt-index is not installed; skipped text/code index")
	}

	if refreshed, err := rebuildTags(root); err != nil {
		if !didWork {
			return err
		}
		fmt.Fprintf(os.Stderr, "ctags failed; kept Zoekt index and skipped symbol refresh: %v\n", err)
	} else if refreshed {
		fmt.Println("Ctags symbols:", tagsFile)
		didWork = true
		symbolIndexed = true
	} else {
		fmt.Fprintln(os.Stderr, "Universal/Exuberant ctags is not installed; skipped symbol index")
	}

	if !didWork {
		return errors.New("no indexer was available; install zoekt-index or ctags")
	}
	if err := markFresh(root, textIndexed, symbolIndexed); err != nil {
		return fmt.Errorf("index built but freshness metadata could not be recorded: %w", err)
	}
	return nil
}

func rebuildTags(root string) (bool, error) {
	ctags, ok := lookCtags()
	if !ok {
		return false, nil
	}

	dir := filepath.Join(root, ".ai-code-index")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	temporary, err := os.CreateTemp(dir, ".tags.*")
	if err != nil {
		return false, err
	}
	temporaryPath := temporary.Name()
	if err := temporary.Close(); err != nil {
		return false, err
	}
	defer os.Remove(temporaryPath)

	args := []string{
		"-R",
		"-f", temporaryPath,
		"--tag-relative=always",
	}
	for _, ignored := range ignoredDirs {
		args = append(args, "--exclude="+ignored)
	}
	args = append(args, ".")
	if err := run(ctags, args, root); err != nil {
		return false, err
	}
	if err := os.Rename(temporaryPath, filepath.Join(dir, "tags")); err != nil {
		return false, err
	}
	return true, nil
}

func removeZoektShards(indexDir string) error {
	entries, err := os.ReadDir(indexDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.Contains(name, ".zoekt") {
			if err := os.Remove(filepath.Join(indexDir, name)); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

func hasZoektIndex(indexDir string) bool {
	matches, err := filepath.Glob(filepath.Join(indexDir, "*.zoekt"))
	return err == nil && len(matches) > 0
}

func resolveRoot(rootFlag string) (string, error) {
	if rootFlag != "" {
		return filepath.Abs(rootFlag)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return findRepoRoot(cwd)
}

func findRepoRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if exists(filepath.Join(dir, ".ai-code-index")) || exists(filepath.Join(dir, ".git")) {
			return dir, nil
		}
		next := filepath.Dir(dir)
		if next == dir {
			return filepath.Abs(start)
		}
		dir = next
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func lookPath(name string) (string, bool) {
	p, err := exec.LookPath(name)
	return p, err == nil
}

func lookCtags() (string, bool) {
	seen := map[string]bool{}
	var candidates []string
	if pathEnv := os.Getenv("PATH"); pathEnv != "" {
		for _, dir := range filepath.SplitList(pathEnv) {
			if dir == "" {
				continue
			}
			candidates = append(candidates, filepath.Join(dir, "ctags"))
		}
	}
	candidates = append(candidates,
		"/opt/homebrew/bin/ctags",
		"/usr/local/bin/ctags",
	)

	for _, candidate := range candidates {
		if seen[candidate] {
			continue
		}
		seen[candidate] = true
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
			continue
		}
		cmd := exec.Command(candidate, "--version")
		out, err := cmd.CombinedOutput()
		if err != nil {
			continue
		}
		versionText := string(out)
		if strings.Contains(versionText, "Universal Ctags") || strings.Contains(versionText, "Exuberant Ctags") {
			return candidate, true
		}
	}
	return "", false
}

func run(bin string, args []string, dir string) error {
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func gitRemoteURL(root string) string {
	git, ok := lookPath("git")
	if !ok {
		return ""
	}
	cmd := exec.Command(git, "-C", root, "config", "--get", "remote.origin.url")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func agentRulesBlock() string {
	return strings.Join([]string{
		"<!-- ai-code-index:start -->",
		"## Local AI Code Index",
		"",
		"- 如果运行环境提供 `local_code_search`，优先使用它进行代码内容、符号、文件和结构化检索，不再直接调用 `.ai-code-index/*.sh`。",
		"- 如果没有 `local_code_search`，再使用 `.ai-code-index/search.sh \"query\"`、`.ai-code-index/struct-search.sh <language> '<pattern>'` 和 `.ai-code-index/symbols.sh \"SymbolName\"`。",
		"- 如果仓库缺少 `.ai-code-index/`，可运行 `ai-code-index init --reindex` 创建本地辅助脚本和索引。",
		"- 工具会检测索引 freshness：Zoekt 过期时自动回退实时 `rg`；ctags 过期时在符号查询前自动刷新。需要恢复 Zoekt 性能时再运行 `ai-code-index reindex`。",
		"- 检索和索引保持本地化；除非用户明确要求，不使用远程 GraphRAG、外部向量库、付费索引或长期语义记忆服务。",
		"<!-- ai-code-index:end -->",
		"",
	}, "\n")
}

func upsertMarkedBlock(path string, block string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	start := "<!-- ai-code-index:start -->"
	end := "<!-- ai-code-index:end -->"

	current, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	text := string(current)
	startIdx := strings.Index(text, start)
	endIdx := strings.Index(text, end)
	if startIdx >= 0 && endIdx >= startIdx {
		endIdx += len(end)
		text = strings.TrimRight(text[:startIdx], "\n") + "\n\n" + strings.TrimSpace(block) + "\n" + text[endIdx:]
	} else if strings.TrimSpace(text) == "" {
		text = strings.TrimSpace(block) + "\n"
	} else {
		text = strings.TrimRight(text, "\n") + "\n\n" + strings.TrimSpace(block) + "\n"
	}
	return os.WriteFile(path, []byte(text), 0o644)
}
