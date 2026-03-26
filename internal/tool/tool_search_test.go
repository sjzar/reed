package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sjzar/reed/internal/security"
)

func realSearchTempDir(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	real, err := filepath.EvalSymlinks(tmp)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

func searchCtx() context.Context {
	return security.WithChecker(context.Background(), security.New(security.ProfileFull, ""))
}

func setupSearchDir(t *testing.T) string {
	t.Helper()
	dir := realSearchTempDir(t)
	os.MkdirAll(filepath.Join(dir, "src"), 0o755)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "src", "app.go"), []byte("package src\nfunc App() string { return \"hello\" }\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "src", "app_test.go"), []byte("package src\nfunc TestApp() {}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("This is a readme\n"), 0o644)
	return dir
}

func newGoSearchTool() Tool {
	return &searchTool{rgPath: ""}
}

func TestSearchTool_FileList(t *testing.T) {
	dir := setupSearchDir(t)
	st := newGoSearchTool()

	t.Run("find all go files", func(t *testing.T) {
		args, _ := json.Marshal(map[string]any{"glob": "**/*.go"})
		ctx := RuntimeContext{Set: true, Cwd: dir}
		pc, err := st.Prepare(searchCtx(), CallRequest{RawArgs: args, Context: ctx})
		if err != nil {
			t.Fatal(err)
		}
		result, err := st.Execute(context.Background(), pc)
		if err != nil {
			t.Fatal(err)
		}
		text := result.Content[0].Text
		if !strings.Contains(text, "main.go") || !strings.Contains(text, "src/app.go") {
			t.Errorf("expected go files in result: %s", text)
		}
	})

	t.Run("no matches", func(t *testing.T) {
		args, _ := json.Marshal(map[string]any{"glob": "**/*.rs"})
		ctx := RuntimeContext{Set: true, Cwd: dir}
		pc, err := st.Prepare(searchCtx(), CallRequest{RawArgs: args, Context: ctx})
		if err != nil {
			t.Fatal(err)
		}
		result, err := st.Execute(context.Background(), pc)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(result.Content[0].Text, "no files found") {
			t.Error("expected no files found message")
		}
	})
}

func TestSearchTool_ContentSearch(t *testing.T) {
	dir := setupSearchDir(t)
	st := newGoSearchTool()

	t.Run("basic search", func(t *testing.T) {
		args, _ := json.Marshal(map[string]any{"pattern": "func main"})
		ctx := RuntimeContext{Set: true, Cwd: dir}
		pc, err := st.Prepare(searchCtx(), CallRequest{RawArgs: args, Context: ctx})
		if err != nil {
			t.Fatal(err)
		}
		result, err := st.Execute(context.Background(), pc)
		if err != nil {
			t.Fatal(err)
		}
		text := result.Content[0].Text
		if !strings.Contains(text, "main.go:2:") {
			t.Errorf("expected match in main.go: %s", text)
		}
	})

	t.Run("ignore case", func(t *testing.T) {
		args, _ := json.Marshal(map[string]any{"pattern": "PACKAGE", "ignore_case": true})
		ctx := RuntimeContext{Set: true, Cwd: dir}
		pc, err := st.Prepare(searchCtx(), CallRequest{RawArgs: args, Context: ctx})
		if err != nil {
			t.Fatal(err)
		}
		result, err := st.Execute(context.Background(), pc)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(result.Content[0].Text, "no matches") {
			t.Error("expected matches with ignore_case")
		}
	})

	t.Run("literal search", func(t *testing.T) {
		args, _ := json.Marshal(map[string]any{"pattern": "func App()", "literal": true})
		ctx := RuntimeContext{Set: true, Cwd: dir}
		pc, err := st.Prepare(searchCtx(), CallRequest{RawArgs: args, Context: ctx})
		if err != nil {
			t.Fatal(err)
		}
		result, err := st.Execute(context.Background(), pc)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(result.Content[0].Text, "no matches") {
			t.Error("expected match for literal pattern")
		}
	})

	t.Run("glob filter", func(t *testing.T) {
		args, _ := json.Marshal(map[string]any{"pattern": "func", "glob": "**/*_test.go"})
		ctx := RuntimeContext{Set: true, Cwd: dir}
		pc, err := st.Prepare(searchCtx(), CallRequest{RawArgs: args, Context: ctx})
		if err != nil {
			t.Fatal(err)
		}
		result, err := st.Execute(context.Background(), pc)
		if err != nil {
			t.Fatal(err)
		}
		text := result.Content[0].Text
		if !strings.Contains(text, "app_test.go") {
			t.Errorf("expected match in test file: %s", text)
		}
		if strings.Contains(text, "main.go") {
			t.Error("should not match non-test files with glob filter")
		}
	})

	t.Run("no matches", func(t *testing.T) {
		args, _ := json.Marshal(map[string]any{"pattern": "nonexistent_string_xyz"})
		ctx := RuntimeContext{Set: true, Cwd: dir}
		pc, err := st.Prepare(searchCtx(), CallRequest{RawArgs: args, Context: ctx})
		if err != nil {
			t.Fatal(err)
		}
		result, err := st.Execute(context.Background(), pc)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(result.Content[0].Text, "no matches") {
			t.Error("expected no matches message")
		}
	})

	t.Run("binary file skipped", func(t *testing.T) {
		binFile := filepath.Join(dir, "binary.dat")
		os.WriteFile(binFile, []byte("hello\x00world"), 0o644)

		args, _ := json.Marshal(map[string]any{"pattern": "hello"})
		ctx := RuntimeContext{Set: true, Cwd: dir}
		pc, err := st.Prepare(searchCtx(), CallRequest{RawArgs: args, Context: ctx})
		if err != nil {
			t.Fatal(err)
		}
		result, err := st.Execute(context.Background(), pc)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(result.Content[0].Text, "binary.dat") {
			t.Error("binary file should be skipped")
		}
	})

	t.Run("files_only mode", func(t *testing.T) {
		args, _ := json.Marshal(map[string]any{"pattern": "package", "files_only": true})
		ctx := RuntimeContext{Set: true, Cwd: dir}
		pc, err := st.Prepare(searchCtx(), CallRequest{RawArgs: args, Context: ctx})
		if err != nil {
			t.Fatal(err)
		}
		result, err := st.Execute(context.Background(), pc)
		if err != nil {
			t.Fatal(err)
		}
		text := result.Content[0].Text
		if !strings.Contains(text, "main.go") {
			t.Errorf("expected main.go in files_only: %s", text)
		}
		if strings.Contains(text, ":2:") {
			t.Error("files_only should not contain line numbers")
		}
	})
}

func TestSearchTool_Validation(t *testing.T) {
	st := newGoSearchTool()

	t.Run("neither pattern nor glob", func(t *testing.T) {
		args, _ := json.Marshal(map[string]any{})
		_, err := st.Prepare(searchCtx(), CallRequest{RawArgs: args})
		if err == nil {
			t.Fatal("expected error when both pattern and glob are empty")
		}
	})

	t.Run("invalid glob", func(t *testing.T) {
		args, _ := json.Marshal(map[string]any{"glob": "[invalid"})
		_, err := st.Prepare(searchCtx(), CallRequest{RawArgs: args})
		if err == nil {
			t.Fatal("expected error for invalid glob")
		}
	})
}

func TestSearchTool_GitignoreRespected_FileList(t *testing.T) {
	dir := realSearchTempDir(t)
	os.MkdirAll(filepath.Join(dir, "build"), 0o755)
	os.WriteFile(filepath.Join(dir, "app.go"), []byte("package main\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "debug.log"), []byte("log data\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "build", "out.bin"), []byte("binary\n"), 0o644)
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.log\nbuild/\n"), 0o644)

	st := newGoSearchTool()
	args, _ := json.Marshal(map[string]any{"glob": "**/*"})
	ctx := RuntimeContext{Set: true, Cwd: dir}
	pc, err := st.Prepare(searchCtx(), CallRequest{RawArgs: args, Context: ctx})
	if err != nil {
		t.Fatal(err)
	}
	result, err := st.Execute(context.Background(), pc)
	if err != nil {
		t.Fatal(err)
	}
	text := result.Content[0].Text
	if strings.Contains(text, "debug.log") {
		t.Error("expected debug.log to be excluded by .gitignore")
	}
	if strings.Contains(text, "out.bin") {
		t.Error("expected build/out.bin to be excluded by .gitignore")
	}
	if !strings.Contains(text, "app.go") {
		t.Error("expected app.go to be included")
	}
}

func TestSearchTool_GitignoreRespected_ContentSearch(t *testing.T) {
	dir := realSearchTempDir(t)
	os.WriteFile(filepath.Join(dir, "app.go"), []byte("package main\nfunc hello() {}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "debug.log"), []byte("func hello() {}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.log\n"), 0o644)

	st := newGoSearchTool()
	args, _ := json.Marshal(map[string]any{"pattern": "func hello"})
	ctx := RuntimeContext{Set: true, Cwd: dir}
	pc, err := st.Prepare(searchCtx(), CallRequest{RawArgs: args, Context: ctx})
	if err != nil {
		t.Fatal(err)
	}
	result, err := st.Execute(context.Background(), pc)
	if err != nil {
		t.Fatal(err)
	}
	text := result.Content[0].Text
	if strings.Contains(text, "debug.log") {
		t.Error("expected debug.log to be excluded by .gitignore")
	}
	if !strings.Contains(text, "app.go") {
		t.Error("expected app.go match to be included")
	}
}

func TestSearchTool_LongLine(t *testing.T) {
	dir := realSearchTempDir(t)
	longLine := strings.Repeat("x", 200*1024) + "FINDME" + strings.Repeat("y", 100)
	os.WriteFile(filepath.Join(dir, "long.txt"), []byte(longLine), 0o644)

	st := newGoSearchTool()
	args, _ := json.Marshal(map[string]any{"pattern": "FINDME"})
	ctx := RuntimeContext{Set: true, Cwd: dir}
	pc, err := st.Prepare(searchCtx(), CallRequest{RawArgs: args, Context: ctx})
	if err != nil {
		t.Fatal(err)
	}
	result, err := st.Execute(context.Background(), pc)
	if err != nil {
		t.Fatal(err)
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "long.txt") {
		t.Errorf("expected match in long.txt (200KB line), got: %s", text[:min(len(text), 200)])
	}
}

func TestSearchTool_WarningDoesNotBlock(t *testing.T) {
	dir := realSearchTempDir(t)
	os.WriteFile(filepath.Join(dir, "good.txt"), []byte("FINDME here\n"), 0o644)
	hugeLine := strings.Repeat("x", 2*1024*1024)
	os.WriteFile(filepath.Join(dir, "huge.txt"), []byte(hugeLine), 0o644)

	st := newGoSearchTool()
	args, _ := json.Marshal(map[string]any{"pattern": "FINDME"})
	ctx := RuntimeContext{Set: true, Cwd: dir}
	pc, err := st.Prepare(searchCtx(), CallRequest{RawArgs: args, Context: ctx})
	if err != nil {
		t.Fatal(err)
	}
	result, err := st.Execute(context.Background(), pc)
	if err != nil {
		t.Fatal(err)
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "good.txt") {
		t.Errorf("expected real match in good.txt, got: %s", text)
	}
	if !strings.Contains(text, "[warning]") {
		t.Error("expected scan warning for huge.txt")
	}
}

func TestSearchTool_CancelledContext_FileList(t *testing.T) {
	dir := setupSearchDir(t)
	st := newGoSearchTool()
	args, _ := json.Marshal(map[string]any{"glob": "**/*.go"})
	ctx := RuntimeContext{Set: true, Cwd: dir}
	pc, err := st.Prepare(searchCtx(), CallRequest{RawArgs: args, Context: ctx})
	if err != nil {
		t.Fatal(err)
	}
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = st.Execute(cancelCtx, pc)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestSearchTool_CancelledContext_ContentSearch(t *testing.T) {
	dir := setupSearchDir(t)
	st := newGoSearchTool()
	args, _ := json.Marshal(map[string]any{"pattern": "func"})
	ctx := RuntimeContext{Set: true, Cwd: dir}
	pc, err := st.Prepare(searchCtx(), CallRequest{RawArgs: args, Context: ctx})
	if err != nil {
		t.Fatal(err)
	}
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = st.Execute(cancelCtx, pc)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestSearchTool_RgBackend(t *testing.T) {
	st := NewSearchTool().(*searchTool)
	if st.rgPath == "" {
		t.Skip("rg not available, skipping rg backend tests")
	}

	dir := setupSearchDir(t)

	t.Run("rg file listing", func(t *testing.T) {
		args, _ := json.Marshal(map[string]any{"glob": "*.go"})
		ctx := RuntimeContext{Set: true, Cwd: dir}
		pc, err := st.Prepare(searchCtx(), CallRequest{RawArgs: args, Context: ctx})
		if err != nil {
			t.Fatal(err)
		}
		result, err := st.Execute(context.Background(), pc)
		if err != nil {
			t.Fatal(err)
		}
		text := result.Content[0].Text
		if !strings.Contains(text, "main.go") {
			t.Errorf("expected main.go in rg file listing: %s", text)
		}
	})

	t.Run("rg content search", func(t *testing.T) {
		args, _ := json.Marshal(map[string]any{"pattern": "func main"})
		ctx := RuntimeContext{Set: true, Cwd: dir}
		pc, err := st.Prepare(searchCtx(), CallRequest{RawArgs: args, Context: ctx})
		if err != nil {
			t.Fatal(err)
		}
		result, err := st.Execute(context.Background(), pc)
		if err != nil {
			t.Fatal(err)
		}
		text := result.Content[0].Text
		if !strings.Contains(text, "main.go") || !strings.Contains(text, "func main") {
			t.Errorf("expected match in main.go: %s", text)
		}
	})

	t.Run("rg files_only", func(t *testing.T) {
		args, _ := json.Marshal(map[string]any{"pattern": "package", "files_only": true})
		ctx := RuntimeContext{Set: true, Cwd: dir}
		pc, err := st.Prepare(searchCtx(), CallRequest{RawArgs: args, Context: ctx})
		if err != nil {
			t.Fatal(err)
		}
		result, err := st.Execute(context.Background(), pc)
		if err != nil {
			t.Fatal(err)
		}
		text := result.Content[0].Text
		if !strings.Contains(text, "main.go") {
			t.Errorf("expected main.go in files_only: %s", text)
		}
	})
}
