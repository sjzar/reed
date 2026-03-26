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

// fullAccessCtx returns a context with a ProfileFull guard (allows everything).
func fullAccessCtx() context.Context {
	return security.WithChecker(context.Background(), security.New(security.ProfileFull, ""))
}

func TestReadTool(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("line1\nline2\nline3\n"), 0o644)

	rt := NewReadTool()
	if rt.Def().Name != "read" {
		t.Fatalf("expected name read, got %s", rt.Def().Name)
	}

	args, _ := json.Marshal(map[string]any{"path": path, "raw": true})
	ctx := fullAccessCtx()
	pc, err := rt.Prepare(ctx, CallRequest{RawArgs: args})
	if err != nil {
		t.Fatalf("unexpected prepare error: %v", err)
	}
	result, err := rt.Execute(ctx, pc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content[0].Text != "line1\nline2\nline3\n" {
		t.Fatalf("unexpected content: %q", result.Content[0].Text)
	}
}

func TestReadToolWithOffset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("a\nb\nc\nd"), 0o644)

	rt := NewReadTool()
	args, _ := json.Marshal(map[string]any{"path": path, "offset": 1, "limit": 2, "raw": true})
	ctx := fullAccessCtx()
	pc, err := rt.Prepare(ctx, CallRequest{RawArgs: args})
	if err != nil {
		t.Fatalf("unexpected prepare error: %v", err)
	}
	result, err := rt.Execute(ctx, pc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content[0].Text != "b\nc" {
		t.Fatalf("expected 'b\\nc', got %q", result.Content[0].Text)
	}
}

func TestReadToolMissingPath(t *testing.T) {
	rt := NewReadTool()
	args, _ := json.Marshal(map[string]any{})
	_, err := rt.Prepare(context.Background(), CallRequest{RawArgs: args})
	if err == nil {
		t.Fatal("expected error for missing path")
	}
}

func TestReadToolNotFound(t *testing.T) {
	rt := NewReadTool()
	args, _ := json.Marshal(map[string]any{"path": "/nonexistent/file.txt"})
	pc, err := rt.Prepare(fullAccessCtx(), CallRequest{RawArgs: args})
	if err != nil {
		t.Fatalf("unexpected prepare error: %v", err)
	}
	_, err = rt.Execute(fullAccessCtx(), pc)
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestWriteTool(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "out.txt")

	wt := NewWriteTool()
	if wt.Def().Name != "write" {
		t.Fatalf("expected name write, got %s", wt.Def().Name)
	}

	args, _ := json.Marshal(map[string]any{"path": path, "content": "hello"})
	ctx := fullAccessCtx()
	pc, err := wt.Prepare(ctx, CallRequest{RawArgs: args})
	if err != nil {
		t.Fatalf("unexpected prepare error: %v", err)
	}
	result, err := wt.Execute(ctx, pc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Content) == 0 || result.Content[0].Text == "" {
		t.Fatal("expected non-empty result")
	}

	data, _ := os.ReadFile(path)
	if string(data) != "hello" {
		t.Fatalf("expected 'hello', got %q", string(data))
	}
}

func TestEditTool(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "edit.txt")
	os.WriteFile(path, []byte("foo bar baz"), 0o644)

	et := NewEditTool()
	if et.Def().Name != "edit" {
		t.Fatalf("expected name edit, got %s", et.Def().Name)
	}

	args, _ := json.Marshal(map[string]any{"path": path, "old_string": "bar", "new_string": "qux"})
	ctx := fullAccessCtx()
	pc, err := et.Prepare(ctx, CallRequest{RawArgs: args})
	if err != nil {
		t.Fatalf("unexpected prepare error: %v", err)
	}
	_, err = et.Execute(ctx, pc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(path)
	if string(data) != "foo qux baz" {
		t.Fatalf("expected 'foo qux baz', got %q", string(data))
	}
}

func TestEditToolNotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "edit.txt")
	os.WriteFile(path, []byte("hello"), 0o644)

	et := NewEditTool()
	args, _ := json.Marshal(map[string]any{"path": path, "old_string": "missing", "new_string": "x"})
	ctx := fullAccessCtx()
	pc, err := et.Prepare(ctx, CallRequest{RawArgs: args})
	if err != nil {
		t.Fatalf("unexpected prepare error: %v", err)
	}
	_, err = et.Execute(ctx, pc)
	if err == nil {
		t.Fatal("expected error for missing old_string")
	}
}

func TestEditToolDuplicate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "edit.txt")
	os.WriteFile(path, []byte("aa aa"), 0o644)

	et := NewEditTool()
	args, _ := json.Marshal(map[string]any{"path": path, "old_string": "aa", "new_string": "bb"})
	ctx := fullAccessCtx()
	pc, err := et.Prepare(ctx, CallRequest{RawArgs: args})
	if err != nil {
		t.Fatalf("unexpected prepare error: %v", err)
	}
	_, err = et.Execute(ctx, pc)
	if err == nil {
		t.Fatal("expected error for duplicate old_string")
	}
}

func TestReadTool_BinaryDetection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "binary.dat")
	os.WriteFile(path, []byte("hello\x00world"), 0o644)

	rt := NewReadTool()
	args, _ := json.Marshal(map[string]any{"path": path})
	pc, err := rt.Prepare(fullAccessCtx(), CallRequest{RawArgs: args})
	if err != nil {
		t.Fatal(err)
	}
	result, err := rt.Execute(fullAccessCtx(), pc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Content[0].Text, "binary file:") {
		t.Errorf("expected binary file message, got %q", result.Content[0].Text)
	}
}

func TestReadTool_ImageFile(t *testing.T) {
	// 1x1 red PNG pixel
	pngData := []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53,
		0xde, 0x00, 0x00, 0x00, 0x0c, 0x49, 0x44, 0x41,
		0x54, 0x08, 0xd7, 0x63, 0xf8, 0xcf, 0xc0, 0x00,
		0x00, 0x00, 0x03, 0x00, 0x01, 0x36, 0x28, 0x19,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e,
		0x44, 0xae, 0x42, 0x60, 0x82,
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "test.png")
	os.WriteFile(path, pngData, 0o644)

	rt := NewReadTool()
	args, _ := json.Marshal(map[string]any{"path": path})
	pc, err := rt.Prepare(fullAccessCtx(), CallRequest{RawArgs: args})
	if err != nil {
		t.Fatal(err)
	}
	result, err := rt.Execute(fullAccessCtx(), pc)
	if err != nil {
		t.Fatal(err)
	}

	// Should return 2 content blocks: text info + image
	if len(result.Content) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(result.Content))
	}
	if result.Content[0].Type != "text" || !strings.Contains(result.Content[0].Text, "test.png") {
		t.Errorf("first block should be text with filename, got %+v", result.Content[0])
	}
	if result.Content[1].Type != "image" {
		t.Errorf("second block should be image, got type %q", result.Content[1].Type)
	}
	if result.Content[1].MIMEType != "image/png" {
		t.Errorf("image MIME should be image/png, got %q", result.Content[1].MIMEType)
	}
	if !strings.HasPrefix(result.Content[1].MediaURI, "data:image/png;base64,") {
		t.Errorf("image MediaURI should be data URI, got %q", result.Content[1].MediaURI[:40])
	}
}

func TestReadTool_PDFFile(t *testing.T) {
	// Minimal PDF that starts with %PDF- (no NUL bytes in first 512 bytes)
	pdfData := []byte("%PDF-1.4\n1 0 obj\n<< /Type /Catalog >>\nendobj\n%%EOF\n")
	dir := t.TempDir()
	path := filepath.Join(dir, "test.pdf")
	os.WriteFile(path, pdfData, 0o644)

	rt := NewReadTool()
	args, _ := json.Marshal(map[string]any{"path": path})
	pc, err := rt.Prepare(fullAccessCtx(), CallRequest{RawArgs: args})
	if err != nil {
		t.Fatal(err)
	}
	result, err := rt.Execute(fullAccessCtx(), pc)
	if err != nil {
		t.Fatal(err)
	}

	// Should return 2 content blocks: text info + document
	if len(result.Content) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(result.Content))
	}
	if result.Content[0].Type != "text" || !strings.Contains(result.Content[0].Text, "test.pdf") {
		t.Errorf("first block should be text with filename, got %+v", result.Content[0])
	}
	if result.Content[1].Type != "document" {
		t.Errorf("second block should be document, got type %q", result.Content[1].Type)
	}
	if result.Content[1].MIMEType != "application/pdf" {
		t.Errorf("document MIME should be application/pdf, got %q", result.Content[1].MIMEType)
	}
	if result.Content[1].Filename != "test.pdf" {
		t.Errorf("document Filename should be test.pdf, got %q", result.Content[1].Filename)
	}
}

func TestReadTool_AccessDenied(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("secret"), 0o644)

	rt := NewReadTool()
	args, _ := json.Marshal(map[string]any{"path": path})
	// Use a restrictive guard that only allows /other/dir
	guard := security.New(security.ProfileWorkdir, "/other/dir")
	ctx := security.WithChecker(context.Background(), guard)
	_, err := rt.Prepare(ctx, CallRequest{
		RawArgs: args,
		Context: RuntimeContext{
			Set: true,
			Cwd: "/other/dir",
		},
	})
	if err == nil {
		t.Fatal("expected access denied error")
	}
}

func TestWriteTool_PreservesPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "perm.txt")
	os.WriteFile(path, []byte("old"), 0o755)

	wt := NewWriteTool()
	args, _ := json.Marshal(map[string]any{"path": path, "content": "new"})
	pc, err := wt.Prepare(fullAccessCtx(), CallRequest{RawArgs: args})
	if err != nil {
		t.Fatal(err)
	}
	_, err = wt.Execute(fullAccessCtx(), pc)
	if err != nil {
		t.Fatal(err)
	}

	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o755 {
		t.Errorf("expected 0755, got %o", info.Mode().Perm())
	}
}

func TestEditTool_PreservesBOM(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bom.txt")
	bom := "\xef\xbb\xbf"
	os.WriteFile(path, []byte(bom+"hello world"), 0o644)

	et := NewEditTool()
	args, _ := json.Marshal(map[string]any{"path": path, "old_string": "hello", "new_string": "goodbye"})
	pc, err := et.Prepare(fullAccessCtx(), CallRequest{RawArgs: args})
	if err != nil {
		t.Fatal(err)
	}
	_, err = et.Execute(fullAccessCtx(), pc)
	if err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(data), bom) {
		t.Error("BOM was not preserved")
	}
	if !strings.Contains(string(data), "goodbye") {
		t.Error("edit was not applied")
	}
}

func TestEditTool_PreservesCRLF(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "crlf.txt")
	os.WriteFile(path, []byte("line1\r\nline2\r\nline3\r\n"), 0o644)

	et := NewEditTool()
	args, _ := json.Marshal(map[string]any{"path": path, "old_string": "line2", "new_string": "modified"})
	pc, err := et.Prepare(fullAccessCtx(), CallRequest{RawArgs: args})
	if err != nil {
		t.Fatal(err)
	}
	_, err = et.Execute(fullAccessCtx(), pc)
	if err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "\r\n") {
		t.Error("CRLF line endings were not preserved")
	}
	if !strings.Contains(string(data), "modified") {
		t.Error("edit was not applied")
	}
}

func TestEditTool_PreservesPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "perm.txt")
	os.WriteFile(path, []byte("hello world"), 0o755)

	et := NewEditTool()
	args, _ := json.Marshal(map[string]any{"path": path, "old_string": "hello", "new_string": "goodbye"})
	pc, err := et.Prepare(fullAccessCtx(), CallRequest{RawArgs: args})
	if err != nil {
		t.Fatal(err)
	}
	_, err = et.Execute(fullAccessCtx(), pc)
	if err != nil {
		t.Fatal(err)
	}

	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o755 {
		t.Errorf("expected 0755, got %o", info.Mode().Perm())
	}
}

func TestEditTool_NoChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "same.txt")
	os.WriteFile(path, []byte("hello world"), 0o644)

	et := NewEditTool()
	args, _ := json.Marshal(map[string]any{"path": path, "old_string": "hello", "new_string": "hello"})
	pc, err := et.Prepare(fullAccessCtx(), CallRequest{RawArgs: args})
	if err != nil {
		t.Fatal(err)
	}
	result, err := et.Execute(fullAccessCtx(), pc)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected IsError=true for no-change edit")
	}
	if !strings.Contains(result.Content[0].Text, "no changes") {
		t.Errorf("expected no changes message, got %q", result.Content[0].Text)
	}
}

func TestLSTool(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0o644)
	os.MkdirAll(filepath.Join(dir, "subdir"), 0o755)

	lt := NewLSTool()

	t.Run("basic listing", func(t *testing.T) {
		args, _ := json.Marshal(map[string]any{"path": dir})
		pc, err := lt.Prepare(fullAccessCtx(), CallRequest{RawArgs: args})
		if err != nil {
			t.Fatal(err)
		}
		result, err := lt.Execute(fullAccessCtx(), pc)
		if err != nil {
			t.Fatal(err)
		}
		text := result.Content[0].Text
		if !strings.Contains(text, "a.txt") || !strings.Contains(text, "b.txt") {
			t.Errorf("expected files in listing: %s", text)
		}
		if !strings.Contains(text, "subdir/") {
			t.Error("expected directory with trailing slash")
		}
	})

	t.Run("large directory uses internal limit", func(t *testing.T) {
		// Internal limit (DefaultLSLimit) is too large to test directly;
		// just verify the tool works without a limit parameter.
		args, _ := json.Marshal(map[string]any{"path": dir})
		pc, err := lt.Prepare(fullAccessCtx(), CallRequest{RawArgs: args})
		if err != nil {
			t.Fatal(err)
		}
		result, err := lt.Execute(fullAccessCtx(), pc)
		if err != nil {
			t.Fatal(err)
		}
		if result.Content[0].Text == "" {
			t.Error("expected non-empty output")
		}
	})

	t.Run("defaults to cwd", func(t *testing.T) {
		args, _ := json.Marshal(map[string]any{})
		_, err := lt.Prepare(fullAccessCtx(), CallRequest{
			RawArgs: args,
			Context: RuntimeContext{Set: true, Cwd: dir},
		})
		if err != nil {
			t.Fatalf("expected success with cwd context: %v", err)
		}
	})
}

func TestEditTool_CRLFMatchWithLFInput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "crlf.txt")
	os.WriteFile(path, []byte("line1\r\nline2\r\nline3\r\n"), 0o644)

	et := NewEditTool()
	args, _ := json.Marshal(map[string]any{"path": path, "old_string": "line1\nline2", "new_string": "changed1\nchanged2"})
	pc, err := et.Prepare(fullAccessCtx(), CallRequest{RawArgs: args})
	if err != nil {
		t.Fatal(err)
	}
	_, err = et.Execute(fullAccessCtx(), pc)
	if err != nil {
		t.Fatalf("expected success matching LF old_string against CRLF file: %v", err)
	}

	data, _ := os.ReadFile(path)
	content := string(data)
	if !strings.Contains(content, "changed1\r\nchanged2") {
		t.Errorf("expected CRLF preserved in output, got %q", content)
	}
}

func TestEditTool_CRLFMatchWithCRLFInput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "crlf.txt")
	os.WriteFile(path, []byte("line1\r\nline2\r\nline3\r\n"), 0o644)

	et := NewEditTool()
	args, _ := json.Marshal(map[string]any{"path": path, "old_string": "line1\r\nline2", "new_string": "changed1\r\nchanged2"})
	pc, err := et.Prepare(fullAccessCtx(), CallRequest{RawArgs: args})
	if err != nil {
		t.Fatal(err)
	}
	_, err = et.Execute(fullAccessCtx(), pc)
	if err != nil {
		t.Fatalf("expected success: %v", err)
	}

	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "changed1\r\nchanged2") {
		t.Error("edit was not applied correctly")
	}
}

func TestReadTool_CancelledContext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("hello"), 0o644)

	rt := NewReadTool()
	args, _ := json.Marshal(map[string]any{"path": path})
	pc, err := rt.Prepare(fullAccessCtx(), CallRequest{RawArgs: args})
	if err != nil {
		t.Fatal(err)
	}
	cancelCtx, cancel := context.WithCancel(fullAccessCtx())
	cancel()
	_, err = rt.Execute(cancelCtx, pc)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestLSTool_CancelledContext(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644)

	lt := NewLSTool()
	args, _ := json.Marshal(map[string]any{"path": dir})
	pc, err := lt.Prepare(fullAccessCtx(), CallRequest{RawArgs: args})
	if err != nil {
		t.Fatal(err)
	}
	cancelCtx, cancel := context.WithCancel(fullAccessCtx())
	cancel()
	_, err = lt.Execute(cancelCtx, pc)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestEditTool_BOMAtStartMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bom.txt")
	bom := "\xef\xbb\xbf"
	os.WriteFile(path, []byte(bom+"first line\nsecond line"), 0o644)

	et := NewEditTool()
	args, _ := json.Marshal(map[string]any{"path": path, "old_string": "first line", "new_string": "replaced line"})
	pc, err := et.Prepare(fullAccessCtx(), CallRequest{RawArgs: args})
	if err != nil {
		t.Fatal(err)
	}
	_, err = et.Execute(fullAccessCtx(), pc)
	if err != nil {
		t.Fatalf("expected success editing BOM file: %v", err)
	}

	data, _ := os.ReadFile(path)
	content := string(data)
	if !strings.HasPrefix(content, bom) {
		t.Error("BOM was not preserved")
	}
	if !strings.Contains(content, "replaced line") {
		t.Error("edit was not applied")
	}
}
