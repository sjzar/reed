package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sjzar/reed/internal/engine"
	"github.com/sjzar/reed/internal/model"
)

func newHTTPWorker() *HTTPWorker {
	return &HTTPWorker{Client: &http.Client{}}
}

func TestHTTPWorker_GetJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"key":"value"}`)
	}))
	defer srv.Close()

	w := newHTTPWorker()
	result := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1", JobID: "j", StepID: "s",
		Uses: "http",
		With: map[string]any{"url": srv.URL},
	})
	if result.Status != model.StepSucceeded {
		t.Fatalf("status = %s, error: %s", result.Status, result.ErrorMessage)
	}
	if result.Outputs["code"] != 200 {
		t.Errorf("code = %v, want 200", result.Outputs["code"])
	}
	r, ok := result.Outputs["result"].(map[string]any)
	if !ok {
		t.Fatalf("result not parsed as map, got %T", result.Outputs["result"])
	}
	if r["key"] != "value" {
		t.Errorf("result.key = %v, want value", r["key"])
	}
}

func TestHTTPWorker_GetPlainText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "hello world")
	}))
	defer srv.Close()

	w := newHTTPWorker()
	result := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1", JobID: "j", StepID: "s",
		Uses: "http",
		With: map[string]any{"url": srv.URL},
	})
	if result.Status != model.StepSucceeded {
		t.Fatalf("status = %s, error: %s", result.Status, result.ErrorMessage)
	}
	if result.Outputs["body"] != "hello world" {
		t.Errorf("body = %q, want hello world", result.Outputs["body"])
	}
	if _, ok := result.Outputs["result"]; ok {
		t.Error("plain text should not produce result")
	}
}

func TestHTTPWorker_PostMapBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content-type = %s, want application/json", ct)
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()

	w := newHTTPWorker()
	result := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1", JobID: "j", StepID: "s",
		Uses: "http",
		With: map[string]any{
			"method": "POST",
			"url":    srv.URL,
			"body":   map[string]any{"foo": "bar"},
		},
	})
	if result.Status != model.StepSucceeded {
		t.Fatalf("status = %s, error: %s", result.Status, result.ErrorMessage)
	}
	r, ok := result.Outputs["result"].(map[string]any)
	if !ok {
		t.Fatalf("result not parsed, got %T", result.Outputs["result"])
	}
	if r["foo"] != "bar" {
		t.Errorf("result.foo = %v, want bar", r["foo"])
	}
}

func TestHTTPWorker_PostStringBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		fmt.Fprint(w, string(data))
	}))
	defer srv.Close()

	w := newHTTPWorker()
	result := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1", JobID: "j", StepID: "s",
		Uses: "http",
		With: map[string]any{
			"method": "POST",
			"url":    srv.URL,
			"body":   "raw payload",
		},
	})
	if result.Status != model.StepSucceeded {
		t.Fatalf("status = %s, error: %s", result.Status, result.ErrorMessage)
	}
	if result.Outputs["body"] != "raw payload" {
		t.Errorf("body = %q, want raw payload", result.Outputs["body"])
	}
}

func TestHTTPWorker_CustomHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, r.Header.Get("X-Custom"))
	}))
	defer srv.Close()

	w := newHTTPWorker()
	result := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1", JobID: "j", StepID: "s",
		Uses: "http",
		With: map[string]any{
			"url":     srv.URL,
			"headers": map[string]any{"X-Custom": "test-val"},
		},
	})
	if result.Status != model.StepSucceeded {
		t.Fatalf("status = %s, error: %s", result.Status, result.ErrorMessage)
	}
	if result.Outputs["body"] != "test-val" {
		t.Errorf("body = %q, want test-val", result.Outputs["body"])
	}
}

func TestHTTPWorker_404DefaultFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		fmt.Fprint(w, "not found")
	}))
	defer srv.Close()

	w := newHTTPWorker()
	result := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1", JobID: "j", StepID: "s",
		Uses: "http",
		With: map[string]any{"url": srv.URL},
	})
	if result.Status != model.StepFailed {
		t.Fatalf("status = %s, want FAILED", result.Status)
	}
	if !strings.Contains(result.ErrorMessage, "404") {
		t.Errorf("error = %q, want to contain 404", result.ErrorMessage)
	}
}

func TestHTTPWorker_404SuccessOnHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		fmt.Fprint(w, "not found")
	}))
	defer srv.Close()

	w := newHTTPWorker()
	result := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1", JobID: "j", StepID: "s",
		Uses: "http",
		With: map[string]any{"url": srv.URL, "success_on_http_error": true},
	})
	if result.Status != model.StepSucceeded {
		t.Fatalf("status = %s, error: %s", result.Status, result.ErrorMessage)
	}
	if result.Outputs["code"] != 404 {
		t.Errorf("code = %v, want 404", result.Outputs["code"])
	}
}

func TestHTTPWorker_NetworkError(t *testing.T) {
	w := newHTTPWorker()
	result := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1", JobID: "j", StepID: "s",
		Uses: "http",
		With: map[string]any{"url": "http://127.0.0.1:1"},
	})
	if result.Status != model.StepFailed {
		t.Fatalf("status = %s, want FAILED", result.Status)
	}
	if !strings.Contains(result.ErrorMessage, "request failed") {
		t.Errorf("error = %q, want to contain 'request failed'", result.ErrorMessage)
	}
}

func TestHTTPWorker_BlockedMIME(t *testing.T) {
	for _, ct := range []string{"video/mp4", "audio/mpeg", "application/octet-stream", "application/zip"} {
		t.Run(ct, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", ct)
				fmt.Fprint(w, "binary data")
			}))
			defer srv.Close()

			w := newHTTPWorker()
			result := w.Execute(context.Background(), engine.StepPayload{
				StepRunID: "sr_1", JobID: "j", StepID: "s",
				Uses: "http",
				With: map[string]any{"url": srv.URL},
			})
			if result.Status != model.StepFailed {
				t.Fatalf("status = %s, want FAILED for %s", result.Status, ct)
			}
			if !strings.Contains(result.ErrorMessage, "blocked content-type") {
				t.Errorf("error = %q", result.ErrorMessage)
			}
		})
	}
}

func TestHTTPWorker_BodyExceedsLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Write more than 10MB
		chunk := make([]byte, 1024*1024) // 1MB
		for i := range chunk {
			chunk[i] = 'x'
		}
		for i := 0; i < 11; i++ {
			w.Write(chunk)
		}
	}))
	defer srv.Close()

	w := newHTTPWorker()
	result := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1", JobID: "j", StepID: "s",
		Uses: "http",
		With: map[string]any{"url": srv.URL},
	})
	if result.Status != model.StepFailed {
		t.Fatalf("status = %s, want FAILED", result.Status)
	}
	if !strings.Contains(result.ErrorMessage, "body size exceeds") {
		t.Errorf("error = %q", result.ErrorMessage)
	}
}

func TestHTTPWorker_OutputFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "file content here")
	}))
	defer srv.Close()

	dir := t.TempDir()
	outPath := filepath.Join(dir, "out.txt")

	w := newHTTPWorker()
	result := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1", JobID: "j", StepID: "s",
		Uses: "http",
		With: map[string]any{"url": srv.URL, "output_file": outPath},
	})
	if result.Status != model.StepSucceeded {
		t.Fatalf("status = %s, error: %s", result.Status, result.ErrorMessage)
	}
	if result.Outputs["filepath"] != outPath {
		t.Errorf("filepath = %v, want %s", result.Outputs["filepath"], outPath)
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	if string(data) != "file content here" {
		t.Errorf("file content = %q", string(data))
	}
}

func TestHTTPWorker_OutputFileMkdir(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	dir := t.TempDir()
	outPath := filepath.Join(dir, "sub", "deep", "out.txt")

	w := newHTTPWorker()
	result := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1", JobID: "j", StepID: "s",
		Uses: "http",
		With: map[string]any{"url": srv.URL, "output_file": outPath},
	})
	if result.Status != model.StepSucceeded {
		t.Fatalf("status = %s, error: %s", result.Status, result.ErrorMessage)
	}
	if _, err := os.Stat(outPath); err != nil {
		t.Errorf("output file not created: %v", err)
	}
}

func TestHTTPWorker_OutputFileRelativePath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "relative")
	}))
	defer srv.Close()

	dir := t.TempDir()

	w := newHTTPWorker()
	result := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1", JobID: "j", StepID: "s",
		Uses:    "http",
		WorkDir: dir,
		With:    map[string]any{"url": srv.URL, "output_file": "rel.txt"},
	})
	if result.Status != model.StepSucceeded {
		t.Fatalf("status = %s, error: %s", result.Status, result.ErrorMessage)
	}
	expected := filepath.Join(dir, "rel.txt")
	if result.Outputs["filepath"] != expected {
		t.Errorf("filepath = %v, want %s", result.Outputs["filepath"], expected)
	}
	data, err := os.ReadFile(expected)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "relative" {
		t.Errorf("content = %q", string(data))
	}
}

func TestHTTPWorker_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "should not reach")
	}))
	defer srv.Close()

	w := newHTTPWorker()
	result := w.Execute(ctx, engine.StepPayload{
		StepRunID: "sr_1", JobID: "j", StepID: "s",
		Uses: "http",
		With: map[string]any{"url": srv.URL},
	})
	if result.Status != model.StepFailed {
		t.Fatalf("status = %s, want FAILED", result.Status)
	}
}

func TestHTTPWorker_ResponseHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Custom-Response", "resp-val")
		fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	w := newHTTPWorker()
	result := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1", JobID: "j", StepID: "s",
		Uses: "http",
		With: map[string]any{"url": srv.URL},
	})
	if result.Status != model.StepSucceeded {
		t.Fatalf("status = %s, error: %s", result.Status, result.ErrorMessage)
	}
	hdrs, ok := result.Outputs["headers"].(map[string]string)
	if !ok {
		t.Fatalf("headers not map[string]string, got %T", result.Outputs["headers"])
	}
	if hdrs["X-Custom-Response"] != "resp-val" {
		t.Errorf("X-Custom-Response = %q, want resp-val", hdrs["X-Custom-Response"])
	}
}

func TestHTTPWorker_MissingURL(t *testing.T) {
	w := newHTTPWorker()
	result := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1", JobID: "j", StepID: "s",
		Uses: "http",
		With: map[string]any{},
	})
	if result.Status != model.StepFailed {
		t.Fatalf("status = %s, want FAILED", result.Status)
	}
	if !strings.Contains(result.ErrorMessage, "url") {
		t.Errorf("error = %q, want to mention url", result.ErrorMessage)
	}
}
