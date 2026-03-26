package tool

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/sjzar/reed/internal/model"
)

// searchLimit is the internal hard cap on results.
// Not exposed in InputSchema — service-level sanitizeResult does final truncation.
const searchLimit = 1000

// ---------------------------------------------------------------------------
// searchTool — unified file and content search
// ---------------------------------------------------------------------------

type searchTool struct {
	rgPath string // empty = use Go fallback
}

// NewSearchTool creates a search tool. Uses rg (ripgrep) when available,
// falls back to Go implementation otherwise.
func NewSearchTool() Tool {
	rgPath, _ := exec.LookPath("rg")
	return &searchTool{rgPath: rgPath}
}

func (t *searchTool) Group() ToolGroup { return GroupCore }

type searchArgs struct {
	Pattern    string `json:"pattern"`
	Glob       string `json:"glob"`
	Path       string `json:"path"`
	Literal    bool   `json:"literal"`
	IgnoreCase bool   `json:"ignore_case"`
	Context    int    `json:"context"`
	FilesOnly  bool   `json:"files_only"`
	absPath    string
}

func (t *searchTool) Def() model.ToolDef {
	return model.ToolDef{
		Name: "search",
		Description: "Search the codebase for files or content. Respects .gitignore, skips binary files.\n" +
			"Use when: finding files by name pattern, searching file contents by regex or literal string.\n" +
			"Don't use when: reading a known file (use read), listing a single directory (use ls).\n" +
			"If pattern is provided: search file contents (regex by default, use literal=true for exact match).\n" +
			"If only glob is provided: list files matching the name/path pattern.\n" +
			`Example: {"pattern": "func main", "glob": "*.go"} or {"glob": "**/*.ts"} or {"pattern": "TODO", "literal": true, "context": 3}`,
		Summary: "Search files by name or content across the codebase.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern":     map[string]any{"type": "string", "description": "Content search pattern (regex unless literal=true). Omit to list files by glob."},
				"glob":        map[string]any{"type": "string", "description": "File name/path filter (e.g. *.go, src/**/*.ts)"},
				"path":        map[string]any{"type": "string", "description": "Directory to search (optional, defaults to cwd)"},
				"literal":     map[string]any{"type": "boolean", "description": "Treat pattern as literal string (default: false)"},
				"ignore_case": map[string]any{"type": "boolean", "description": "Case-insensitive search (default: false)"},
				"context":     map[string]any{"type": "integer", "description": "Lines of context before and after each match (default: 0)"},
				"files_only":  map[string]any{"type": "boolean", "description": "Return only matching file paths for content search (default: false)"},
			},
		},
	}
}

func (t *searchTool) Prepare(ctx context.Context, req CallRequest) (*PreparedCall, error) {
	var a searchArgs
	if err := json.Unmarshal(req.RawArgs, &a); err != nil {
		return nil, fmt.Errorf("parse search args: %w", err)
	}
	if a.Pattern == "" && a.Glob == "" {
		return nil, fmt.Errorf("either pattern or glob is required")
	}
	if a.Glob != "" {
		if _, err := doublestar.Match(a.Glob, ""); err != nil {
			return nil, fmt.Errorf("invalid glob: %w. Example: *.go, src/**/*.ts", err)
		}
	}
	if a.Context < 0 {
		a.Context = 0
	}
	if a.Path == "" {
		a.Path = req.Context.Cwd
	}
	if a.Path == "" {
		return nil, fmt.Errorf("path is required (no cwd available)")
	}
	resolved, err := resolveFSPath(req.Context, a.Path)
	if err != nil {
		return nil, err
	}
	if err := checkReadAccess(ctx, resolved); err != nil {
		return nil, err
	}
	a.absPath = resolved
	return &PreparedCall{
		ToolCallID: req.ToolCallID,
		Name:       req.Name,
		RawArgs:    req.RawArgs,
		Parsed:     a,
		Plan: ExecutionPlan{
			Mode:    ExecModeSync,
			Timeout: DefaultTimeout,
			Policy:  ParallelSafe,
		},
	}, nil
}

func (t *searchTool) Execute(ctx context.Context, call *PreparedCall) (*Result, error) {
	a, ok := call.Parsed.(searchArgs)
	if !ok {
		return nil, fmt.Errorf("internal: unexpected parsed type %T", call.Parsed)
	}
	if t.rgPath != "" {
		return t.executeWithRg(ctx, a)
	}
	return t.executeWithGo(ctx, a)
}

// ---------------------------------------------------------------------------
// rg backend
// ---------------------------------------------------------------------------

func (t *searchTool) executeWithRg(ctx context.Context, a searchArgs) (*Result, error) {
	args := t.buildRgArgs(a)
	cmd := exec.CommandContext(ctx, t.rgPath, args...)
	cmd.Dir = a.absPath

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
			err = nil
		}
	}
	if err != nil {
		return nil, fmt.Errorf("rg: %w", err)
	}

	output := stdout.String()

	// rg exit code 1 = no matches (not an error)
	if exitCode == 1 {
		if a.Pattern == "" {
			return TextResult("no files found matching pattern"), nil
		}
		return TextResult("no matches found"), nil
	}
	// rg exit code 2 = error
	if exitCode == 2 {
		return nil, fmt.Errorf("rg error: %s", strings.TrimSpace(stderr.String()))
	}

	if output == "" {
		if a.Pattern == "" {
			return TextResult("no files found matching pattern"), nil
		}
		return TextResult("no matches found"), nil
	}

	// Make paths relative to absPath for consistency with Go fallback.
	output = t.relativizeRgOutput(output, a)

	return TextResult(output), nil
}

func (t *searchTool) buildRgArgs(a searchArgs) []string {
	var args []string

	if a.Pattern == "" {
		// File listing mode (like find)
		args = append(args, "--files")
		if a.Glob != "" {
			args = append(args, "-g", a.Glob)
		}
	} else {
		// Content search mode (like grep)
		args = append(args, "--no-heading", "--line-number", "--color", "never")
		args = append(args, "--max-count", strconv.Itoa(searchLimit))
		if a.Literal {
			args = append(args, "-F")
		}
		if a.IgnoreCase {
			args = append(args, "-i")
		}
		if a.Context > 0 {
			args = append(args, "-C", strconv.Itoa(a.Context))
		}
		if a.FilesOnly {
			args = append(args, "-l")
		}
		if a.Glob != "" {
			args = append(args, "-g", a.Glob)
		}
		args = append(args, "--", a.Pattern)
	}

	args = append(args, ".")
	return args
}

// relativizeRgOutput strips the "./" prefix from rg output paths.
func (t *searchTool) relativizeRgOutput(output string, a searchArgs) string {
	// rg with "." as path prefixes with "./" — strip it for consistency.
	output = strings.ReplaceAll(output, "./", "")
	return output
}

// ---------------------------------------------------------------------------
// Go fallback
// ---------------------------------------------------------------------------

func (t *searchTool) executeWithGo(ctx context.Context, a searchArgs) (*Result, error) {
	if a.Pattern == "" {
		return searchFilesGo(ctx, a.absPath, a.Glob)
	}
	return searchContentGo(ctx, a)
}

// searchFilesGo lists files matching a glob pattern (Go fallback for find).
func searchFilesGo(ctx context.Context, absPath, glob string) (*Result, error) {
	var matches []string
	ignore := loadGitignore(absPath)

	err := filepath.WalkDir(absPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() && isIgnoredDir(d.Name()) {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(absPath, path)
		if err != nil {
			return nil
		}
		if ignore != nil && rel != "." && ignore.Match(rel, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		matched, err := doublestar.PathMatch(glob, rel)
		if err != nil {
			return nil
		}
		if matched {
			matches = append(matches, rel)
			if len(matches) >= searchLimit+1 {
				return filepath.SkipAll
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk directory: %w", err)
	}

	truncated := false
	if len(matches) > searchLimit {
		matches = matches[:searchLimit]
		truncated = true
	}

	if len(matches) == 0 {
		return TextResult("no files found matching pattern"), nil
	}

	var b strings.Builder
	for _, m := range matches {
		b.WriteString(m)
		b.WriteByte('\n')
	}
	if truncated {
		fmt.Fprintf(&b, "\n[truncated: showing %d results, more available]", searchLimit)
	}
	return TextResult(b.String()), nil
}

// searchContentGo searches file contents for a pattern (Go fallback for grep).
func searchContentGo(ctx context.Context, a searchArgs) (*Result, error) {
	// Build regex
	pattern := a.Pattern
	if a.Literal {
		pattern = regexp.QuoteMeta(pattern)
	}
	if a.IgnoreCase {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid regex pattern: %w. Use literal=true for exact string matching, or escape special regex characters", err)
	}

	var results []string
	var matchedFiles []string
	var warnings []string
	const maxWarnings = 5
	warningOverflow := 0
	matchCount := 0
	truncated := false
	ignore := loadGitignore(a.absPath)

	walkErr := filepath.WalkDir(a.absPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if isIgnoredDir(d.Name()) {
				return filepath.SkipDir
			}
			if ignore != nil {
				rel, relErr := filepath.Rel(a.absPath, path)
				if relErr == nil && rel != "." && ignore.Match(rel, true) {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}

		if ignore != nil {
			rel, relErr := filepath.Rel(a.absPath, path)
			if relErr == nil && ignore.Match(rel, false) {
				return nil
			}
		}

		rel, _ := filepath.Rel(a.absPath, path)
		if a.Glob != "" {
			matched, err := doublestar.PathMatch(a.Glob, rel)
			if err != nil || !matched {
				return nil
			}
		}

		// Skip binary files
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()

		peek := make([]byte, 512)
		n, _ := f.Read(peek)
		if bytes.ContainsRune(peek[:n], 0) {
			return nil
		}
		if _, err := f.Seek(0, 0); err != nil {
			return nil
		}

		// files_only mode
		if a.FilesOnly {
			scanner := bufio.NewScanner(f)
			scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
			for scanner.Scan() {
				if re.MatchString(scanner.Text()) {
					matchedFiles = append(matchedFiles, rel)
					matchCount++
					if matchCount >= searchLimit {
						truncated = true
						return filepath.SkipAll
					}
					break
				}
			}
			return nil
		}

		// Content mode with optional context
		fileMatches := searchFileWithContext(f, re, rel, a.Context, searchLimit-matchCount)
		results = append(results, fileMatches.lines...)
		matchCount += fileMatches.matchCount
		if matchCount >= searchLimit {
			truncated = true
			return filepath.SkipAll
		}

		if err := fileMatches.scanErr; err != nil {
			if len(warnings) < maxWarnings {
				warnings = append(warnings, fmt.Sprintf("[warning] %s: scan error: %s", rel, err))
			} else {
				warningOverflow++
			}
		}
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("walk directory: %w", walkErr)
	}

	var b strings.Builder

	if a.FilesOnly {
		if len(matchedFiles) == 0 {
			return TextResult("no matches found"), nil
		}
		for _, f := range matchedFiles {
			b.WriteString(f)
			b.WriteByte('\n')
		}
	} else {
		if len(results) == 0 && len(warnings) == 0 {
			return TextResult("no matches found"), nil
		}
		for _, r := range results {
			b.WriteString(r)
			b.WriteByte('\n')
		}
	}

	if truncated {
		fmt.Fprintf(&b, "\n[truncated: showing %d matches, more available]", matchCount)
	}
	for _, w := range warnings {
		b.WriteByte('\n')
		b.WriteString(w)
	}
	if warningOverflow > 0 {
		fmt.Fprintf(&b, "\n[%d more warnings omitted]", warningOverflow)
	}
	return TextResult(b.String()), nil
}

// ---------------------------------------------------------------------------
// searchFileWithContext — content search with context lines (Go fallback)
// ---------------------------------------------------------------------------

type searchFileResult struct {
	lines      []string
	matchCount int
	scanErr    error
}

// searchFileWithContext scans a file for regex matches, optionally including context lines.
// Uses a ring buffer for before-context and a counter for after-context.
func searchFileWithContext(f *os.File, re *regexp.Regexp, rel string, ctxLines int, maxMatches int) searchFileResult {
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var result searchFileResult

	if ctxLines <= 0 {
		// Fast path: no context
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := scanner.Text()
			if re.MatchString(line) {
				result.lines = append(result.lines, fmt.Sprintf("%s:%d:%s", rel, lineNum, line))
				result.matchCount++
				if result.matchCount >= maxMatches {
					break
				}
			}
		}
		result.scanErr = scanner.Err()
		return result
	}

	// Context mode: ring buffer for before-context, counter for after-context
	type numberedLine struct {
		num  int
		text string
	}

	ring := make([]numberedLine, ctxLines)
	ringLen := 0

	afterRemaining := 0
	lastEmittedLine := 0
	needSeparator := false

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		isMatch := re.MatchString(line)

		// After-context of previous match (but not if this line is itself a match)
		if afterRemaining > 0 && !isMatch {
			result.lines = append(result.lines, fmt.Sprintf("%s:%d-%s", rel, lineNum, line))
			lastEmittedLine = lineNum
			afterRemaining--
		}

		if isMatch {
			afterRemaining = 0
			// Emit before-context from ring buffer
			for i := 0; i < ringLen; i++ {
				idx := (len(ring) + (lineNum-1)%len(ring) - ringLen + 1 + i) % len(ring)
				entry := ring[idx]
				if entry.num > lastEmittedLine {
					if needSeparator && entry.num > lastEmittedLine+1 {
						result.lines = append(result.lines, "--")
					}
					result.lines = append(result.lines, fmt.Sprintf("%s:%d-%s", rel, entry.num, entry.text))
					lastEmittedLine = entry.num
				}
			}

			if needSeparator && lineNum > lastEmittedLine+1 {
				result.lines = append(result.lines, "--")
			}

			result.lines = append(result.lines, fmt.Sprintf("%s:%d:%s", rel, lineNum, line))
			lastEmittedLine = lineNum
			needSeparator = true
			afterRemaining = ctxLines

			result.matchCount++
			if result.matchCount >= maxMatches {
				break
			}
		} else {
			ring[lineNum%len(ring)] = numberedLine{num: lineNum, text: line}
			if ringLen < ctxLines {
				ringLen++
			}
		}
	}
	result.scanErr = scanner.Err()
	return result
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// isIgnoredDir returns true for common directories that should be skipped during search.
func isIgnoredDir(name string) bool {
	switch name {
	case ".git", ".svn", ".hg", "node_modules", "__pycache__", ".tox", ".mypy_cache", "vendor":
		return true
	}
	return false
}
