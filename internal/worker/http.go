package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sjzar/reed/internal/engine"
	"github.com/sjzar/reed/internal/model"
)

// HTTPWorker executes steps by making HTTP requests.
type HTTPWorker struct {
	Client *http.Client
}

// Ensure HTTPWorker implements engine.Worker.
var _ engine.Worker = (*HTTPWorker)(nil)

// blockedMIMEPrefixes are content types that must not be read into memory.
var blockedMIMEPrefixes = []string{
	"video/",
	"audio/",
	"application/octet-stream",
	"application/zip",
}

func (w *HTTPWorker) Execute(ctx context.Context, p engine.StepPayload) engine.StepRunResult {
	result := newResult(p)

	emitter := newStepEmitter(p)

	fail := func(msg string) engine.StepRunResult {
		result.Status = model.StepFailed
		result.ErrorMessage = msg
		emitter.PublishStatusf("http_end status=failed error=%s", msg)
		return result
	}

	// Extract parameters from With
	method, _ := p.With["method"].(string)
	if method == "" {
		method = http.MethodGet
	}
	method = strings.ToUpper(method)

	rawURL, _ := p.With["url"].(string)
	if rawURL == "" {
		return fail("http worker: 'url' is required")
	}

	outputFile, _ := p.With["output_file"].(string)
	successOnHTTPError, _ := p.With["success_on_http_error"].(bool)

	// Build request body
	var bodyReader io.Reader
	autoJSON := false
	switch b := p.With["body"].(type) {
	case string:
		bodyReader = strings.NewReader(b)
	case map[string]any:
		data, err := json.Marshal(b)
		if err != nil {
			return fail("http worker: failed to marshal body: " + err.Error())
		}
		bodyReader = strings.NewReader(string(data))
		autoJSON = true
	case nil:
		// no body
	default:
		data, err := json.Marshal(b)
		if err != nil {
			return fail("http worker: failed to marshal body: " + err.Error())
		}
		bodyReader = strings.NewReader(string(data))
		autoJSON = true
	}

	req, err := http.NewRequestWithContext(ctx, method, rawURL, bodyReader)
	if err != nil {
		return fail("http worker: " + err.Error())
	}

	if autoJSON {
		req.Header.Set("Content-Type", "application/json")
	}

	// Set custom headers
	if hdrs, ok := p.With["headers"].(map[string]any); ok {
		for k, v := range hdrs {
			if s, ok := v.(string); ok {
				req.Header.Set(k, s)
			}
		}
	}

	client := w.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	emitter.PublishStatusf("http_start method=%s url=%s", method, rawURL)

	start := time.Now()
	resp, err := client.Do(req)
	elapsed := time.Since(start)
	if err != nil {
		return fail("http worker: request failed: " + err.Error())
	}
	defer resp.Body.Close()

	emitter.PublishStatusf("http_response code=%d duration=%s", resp.StatusCode, elapsed)

	result.Outputs["code"] = resp.StatusCode
	result.Outputs["duration"] = elapsed.String()

	// Collect response headers
	respHeaders := make(map[string]string)
	for k := range resp.Header {
		respHeaders[k] = resp.Header.Get(k)
	}
	result.Outputs["headers"] = respHeaders

	if outputFile != "" {
		// Branch B: stream to file
		path := outputFile
		if !filepath.IsAbs(path) && p.WorkDir != "" {
			path = filepath.Join(p.WorkDir, path)
		}
		if !filepath.IsAbs(path) {
			resolved, err := resolveAbsPath(path)
			if err != nil {
				return fail("http worker: bad output_file path: " + err.Error())
			}
			path = resolved
		}

		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fail("http worker: mkdir for output_file: " + err.Error())
		}

		f, err := os.Create(path)
		if err != nil {
			return fail("http worker: create output_file: " + err.Error())
		}
		if _, err := io.Copy(f, resp.Body); err != nil {
			f.Close()
			return fail("http worker: write output_file: " + err.Error())
		}
		f.Close()

		result.Outputs["filepath"] = path
	} else {
		// Branch A: read into memory with limit check
		ct := resp.Header.Get("Content-Type")
		ctLower := strings.ToLower(ct)
		for _, prefix := range blockedMIMEPrefixes {
			if strings.HasPrefix(ctLower, prefix) {
				return fail(fmt.Sprintf("http worker: blocked content-type %q; use 'output_file' for binary downloads", ct))
			}
		}

		// Read limit+1 bytes; if we get that many, the body exceeds the limit.
		limitPlusOne := int64(maxOutputBytes) + 1
		bodyBuf := newLimitedWriter(maxOutputBytes)
		var reader io.Reader = io.LimitReader(resp.Body, limitPlusOne)
		dst := emitter.OutputWriter(bodyBuf)
		n, err := io.Copy(dst, reader)
		if err != nil {
			return fail("http worker: read body: " + err.Error())
		}
		if n >= limitPlusOne {
			return fail("http worker: body size exceeds in-memory limit; use 'output_file' for large downloads")
		}

		body := bodyBuf.String()
		result.Outputs["body"] = body

		// JSON auto-parse: object or array only
		if parsed, ok := tryParseJSONOutput(body); ok {
			result.Outputs["result"] = parsed
		}
	}

	// HTTP error status check
	if resp.StatusCode >= 400 && !successOnHTTPError {
		result.Status = model.StepFailed
		result.ErrorMessage = fmt.Sprintf("http worker: HTTP %d", resp.StatusCode)
		emitter.PublishStatusf("http_end status=failed code=%d", resp.StatusCode)
		return result
	}

	emitter.PublishStatusf("http_end status=succeeded code=%d duration=%s", resp.StatusCode, elapsed)
	result.Status = model.StepSucceeded
	return result
}
