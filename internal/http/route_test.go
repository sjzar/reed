package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sjzar/reed/internal/model"
)

// mockProvider implements StatusProvider for testing.
type mockProvider struct {
	processID string
	pid       int
	mode      string
}

func (m *mockProvider) PingData() model.PingResponse {
	return model.PingResponse{
		ProcessID: m.processID,
		PID:       m.pid,
		Mode:      m.mode,
		Now:       "2026-01-01T00:00:00Z",
	}
}

func (m *mockProvider) StatusData() (any, error) {
	return model.StatusView{
		ProcessID: m.processID,
		PID:       m.pid,
		Mode:      m.mode,
		Status:    "RUNNING",
	}, nil
}

func (m *mockProvider) RunData(runID string) (any, bool) {
	if runID == "run_found" {
		return map[string]string{"runID": runID, "status": "RUNNING"}, true
	}
	return nil, false
}

func (m *mockProvider) StopRun(runID string) bool {
	return runID == "run_found"
}

func newTestService() *Service {
	s := New(Config{Debug: false})
	s.SetStatusProvider(&mockProvider{
		processID: "proc_test1234_ab12",
		pid:       42,
		mode:      "cli",
	})
	return s
}

func TestHealthEndpoint(t *testing.T) {
	s := newTestService()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/health", nil)
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("expected status ok, got %s", body["status"])
	}
}

func TestPingEndpoint(t *testing.T) {
	s := newTestService()

	for _, path := range []string{"/v1/ping", "/api/v1/ping"} {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", path, nil)
		req.Header.Set("Accept", "application/json")
		s.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d", path, w.Code)
		}
		var resp struct {
			Code int                `json:"code"`
			Data model.PingResponse `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("%s: invalid JSON: %v", path, err)
		}
		if resp.Data.ProcessID != "proc_test1234_ab12" {
			t.Fatalf("%s: wrong processID: %s", path, resp.Data.ProcessID)
		}
	}
}

func TestStatusEndpoint(t *testing.T) {
	s := newTestService()

	for _, path := range []string{"/v1/status", "/api/v1/status"} {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", path, nil)
		req.Header.Set("Accept", "application/json")
		s.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d", path, w.Code)
		}
		var resp struct {
			Code int              `json:"code"`
			Data model.StatusView `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("%s: invalid JSON: %v", path, err)
		}
		if resp.Data.ProcessID != "proc_test1234_ab12" {
			t.Fatalf("%s: wrong processID: %s", path, resp.Data.ProcessID)
		}
		if resp.Data.Status != "RUNNING" {
			t.Fatalf("%s: wrong status: %s", path, resp.Data.Status)
		}
	}
}

// errProvider returns an error from StatusData.
type errProvider struct {
	mockProvider
	statusErr error
}

func (e *errProvider) StatusData() (any, error) {
	return nil, e.statusErr
}

func TestPingNoProvider(t *testing.T) {
	s := New(Config{Debug: false})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/ping", nil)
	req.Header.Set("Accept", "application/json")
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestStatusNoProvider(t *testing.T) {
	s := New(Config{Debug: false})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/status", nil)
	req.Header.Set("Accept", "application/json")
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestStatusProviderError(t *testing.T) {
	s := New(Config{Debug: false})
	s.SetStatusProvider(&errProvider{
		mockProvider: mockProvider{processID: "proc_test", pid: 1, mode: "cli"},
		statusErr:    fmt.Errorf("db unavailable"),
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/status", nil)
	req.Header.Set("Accept", "application/json")
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	msg, _ := body["message"].(string)
	if !strings.Contains(msg, "db unavailable") {
		t.Errorf("expected wrapped error in message, got %q", msg)
	}
}

func TestNocacheMiddleware(t *testing.T) {
	s := newTestService()
	for _, path := range []string{"/api/v1/ping", "/api/v1/status"} {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", path, nil)
		req.Header.Set("Accept", "application/json")
		s.router.ServeHTTP(w, req)

		cc := w.Header().Get("Cache-Control")
		if !strings.Contains(cc, "no-cache") {
			t.Errorf("%s: expected no-cache header, got %q", path, cc)
		}
		if w.Header().Get("Pragma") != "no-cache" {
			t.Errorf("%s: expected Pragma: no-cache", path)
		}
		if w.Header().Get("Expires") != "0" {
			t.Errorf("%s: expected Expires: 0", path)
		}
	}
}

func TestNocacheMiddleware_NotOnV1(t *testing.T) {
	// /v1/* routes do NOT use nocacheMiddleware
	s := newTestService()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/ping", nil)
	req.Header.Set("Accept", "application/json")
	s.router.ServeHTTP(w, req)

	if w.Header().Get("Cache-Control") != "" {
		t.Errorf("expected no Cache-Control on /v1/ping, got %q", w.Header().Get("Cache-Control"))
	}
}

func TestIsIPC_FalseForNormalHTTP(t *testing.T) {
	var captured bool
	s := New(Config{Debug: false})
	s.router.GET("/test-ipc", func(c *gin.Context) {
		captured = IsIPC(c)
		c.Status(http.StatusOK)
	})
	// Use a plain httptest request — no ipcConnContext, so IsIPC must be false.
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test-ipc", nil)
	s.router.ServeHTTP(w, req)
	if captured {
		t.Error("expected IsIPC=false for normal HTTP request")
	}
}

func TestIpcConnContext(t *testing.T) {
	base := context.Background()
	ctx := ipcConnContext(base, nil)
	v, ok := ctx.Value(ipcCtxKey{}).(bool)
	if !ok || !v {
		t.Error("ipcConnContext must inject true into context")
	}
}

func TestRespondAndRespondError_IPCFormat(t *testing.T) {
	s := New(Config{Debug: false})

	// Register test routes that simulate IPC requests by injecting the flag.
	s.router.GET("/test-respond", func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), ipcCtxKey{}, true)
		c.Request = c.Request.WithContext(ctx)
		s.Respond(c, "hello")
	})
	s.router.GET("/test-respond-error", func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), ipcCtxKey{}, true)
		c.Request = c.Request.WithContext(ctx)
		s.RespondError(c, fmt.Errorf("something failed"))
	})

	t.Run("Respond JSON with IPC header", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/test-respond", nil)
		s.router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		if w.Header().Get("X-Reed-IPC") != "true" {
			t.Error("expected X-Reed-IPC header")
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("expected JSON: %v", err)
		}
		if body["data"] != "hello" {
			t.Errorf("expected data=hello, got %v", body["data"])
		}
	})

	t.Run("RespondError JSON with IPC header", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/test-respond-error", nil)
		s.router.ServeHTTP(w, req)
		if w.Header().Get("X-Reed-IPC") != "true" {
			t.Error("expected X-Reed-IPC header")
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("expected JSON: %v", err)
		}
		if msg, _ := body["message"].(string); !strings.Contains(msg, "something failed") {
			t.Errorf("expected error message, got %v", body["message"])
		}
	})
}

func TestRespondAndRespondError_HTTPFormat(t *testing.T) {
	s := New(Config{Debug: false})

	s.router.GET("/test-http-respond", func(c *gin.Context) {
		// No IPC flag — simulates HTTP connection
		s.Respond(c, "world")
	})
	s.router.GET("/test-http-error", func(c *gin.Context) {
		s.RespondError(c, fmt.Errorf("json error"))
	})

	t.Run("Respond JSON without IPC header", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/test-http-respond", nil)
		s.router.ServeHTTP(w, req)
		if w.Header().Get("X-Reed-IPC") != "" {
			t.Error("unexpected X-Reed-IPC header on HTTP connection")
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("expected JSON: %v", err)
		}
	})

	t.Run("RespondError JSON without IPC header", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/test-http-error", nil)
		s.router.ServeHTTP(w, req)
		if w.Header().Get("X-Reed-IPC") != "" {
			t.Error("unexpected X-Reed-IPC header on HTTP connection")
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("expected JSON: %v", err)
		}
	})
}

func TestShutdown_NilServer(t *testing.T) {
	s := New(Config{Debug: false})
	// server is nil — must be a no-op
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestShutdownIPC_NilServer(t *testing.T) {
	s := New(Config{Debug: false})
	// ipcSrv is nil — must be a no-op
	if err := s.ShutdownIPC(context.Background()); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestGetRunEndpoint(t *testing.T) {
	s := newTestService()

	t.Run("found", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/v1/runs/run_found", nil)
		req.Header.Set("Accept", "application/json")
		s.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("not found", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/v1/runs/run_missing", nil)
		req.Header.Set("Accept", "application/json")
		s.router.ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("no provider", func(t *testing.T) {
		s2 := New(Config{Debug: false})
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/v1/runs/run_found", nil)
		req.Header.Set("Accept", "application/json")
		s2.router.ServeHTTP(w, req)

		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503, got %d", w.Code)
		}
	})
}

func TestStopRunEndpoint(t *testing.T) {
	s := newTestService()

	t.Run("found", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/v1/runs/run_found/stop", nil)
		req.Header.Set("Accept", "application/json")
		s.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var resp struct {
			Code int `json:"code"`
			Data struct {
				RunID   string `json:"runID"`
				Stopped bool   `json:"stopped"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		if !resp.Data.Stopped {
			t.Error("expected stopped=true")
		}
	})

	t.Run("not found", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/v1/runs/run_missing/stop", nil)
		req.Header.Set("Accept", "application/json")
		s.router.ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
		}
	})
}

func TestStartIPC_RealSocket(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")

	s := newTestService()
	if err := s.StartIPC(sockPath); err != nil {
		t.Fatalf("StartIPC failed: %v", err)
	}
	defer s.ShutdownIPC(context.Background())

	// Verify the socket file exists and is reachable.
	conn, err := net.DialTimeout("unix", sockPath, time.Second)
	if err != nil {
		t.Fatalf("could not connect to IPC socket: %v", err)
	}
	conn.Close()

	// Verify the socket file is removed after shutdown.
	if err := s.ShutdownIPC(context.Background()); err != nil {
		t.Fatalf("ShutdownIPC error: %v", err)
	}
	if _, err := os.Stat(sockPath); !os.IsNotExist(err) {
		t.Error("expected socket file to be removed after ShutdownIPC")
	}
}
