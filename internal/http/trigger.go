package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/spf13/cast"

	"github.com/sjzar/reed/internal/engine"
	reederrors "github.com/sjzar/reed/internal/errors"
	"github.com/sjzar/reed/internal/media"
	"github.com/sjzar/reed/internal/model"
)

// RunResolver resolves trigger params into a RunRequest.
type RunResolver interface {
	ResolveRunRequest(ctx context.Context, wf *model.Workflow, wfSource string, params model.TriggerParams) (*model.RunRequest, error)
}

// RunSubmitter submits runs and waits for their completion.
type RunSubmitter interface {
	Submit(req *model.RunRequest) (*engine.RunHandle, error)
	WaitRun(ctx context.Context, runID string) (*engine.RunView, error)
}

// TriggerMediaUploader is the narrow interface for uploading media in HTTP triggers.
type TriggerMediaUploader interface {
	Upload(ctx context.Context, r io.Reader, mimeType string) (*model.MediaEntry, error)
}

// HTTPTriggerDispatcher implements TriggerDispatcher.
// It converts HTTP requests into RunRequests via the run Service.
type HTTPTriggerDispatcher struct {
	resolver  RunResolver
	submitter RunSubmitter
	wf        *model.Workflow
	wfSource  string
	workDir   string // project working directory; set on req.WorkDir
	media     TriggerMediaUploader

	concurrency *concurrencyController
}

// NewHTTPTriggerDispatcher creates a dispatcher for HTTP-triggered runs.
func NewHTTPTriggerDispatcher(
	resolver RunResolver,
	submitter RunSubmitter,
	wf *model.Workflow,
	wfSource string,
	workDir string,
	mediaUploader TriggerMediaUploader,
) *HTTPTriggerDispatcher {
	return &HTTPTriggerDispatcher{
		resolver:    resolver,
		submitter:   submitter,
		wf:          wf,
		wfSource:    wfSource,
		workDir:     workDir,
		media:       mediaUploader,
		concurrency: newConcurrencyController(),
	}
}

// TriggerResponse is the unified response envelope for HTTP-triggered runs.
type TriggerResponse struct {
	Code      int            `json:"code"`
	RunID     string         `json:"runID"`
	Status    string         `json:"status"`
	Outputs   map[string]any `json:"outputs,omitempty"`
	ErrorCode string         `json:"errorCode,omitempty"`
	Message   string         `json:"message,omitempty"`
}

func respondTrigger(c *gin.Context, httpStatus int, runID, status string, outputs map[string]any, errorCode, message string) {
	if outputs == nil {
		outputs = make(map[string]any)
	}
	c.JSON(httpStatus, TriggerResponse{
		Code:      httpStatus,
		RunID:     runID,
		Status:    status,
		Outputs:   outputs,
		ErrorCode: errorCode,
		Message:   message,
	})
}

// HandleHTTPTrigger handles an incoming HTTP trigger request.
func (d *HTTPTriggerDispatcher) HandleHTTPTrigger(route model.HTTPRoute, c *gin.Context) {
	// 1. Parse request body as input values
	var inputValues map[string]any
	contentType := c.ContentType()

	switch {
	case strings.HasPrefix(contentType, "multipart/form-data"):
		// Multipart: parse form fields + file uploads (10MB total body limit)
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 10<<20)
		parsed, err := d.parseMultipartInputs(c)
		if err != nil {
			respondTrigger(c, http.StatusBadRequest, "", "", nil, "", fmt.Sprintf("parse multipart: %v", err))
			return
		}
		inputValues = parsed

	default:
		// JSON body (existing behavior)
		if c.Request.Body != nil && c.Request.ContentLength != 0 {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 10<<20) // 10MB limit
			if err := json.NewDecoder(c.Request.Body).Decode(&inputValues); err != nil && !errors.Is(err, io.EOF) {
				respondTrigger(c, http.StatusBadRequest, "", "", nil, "", fmt.Sprintf("invalid request body: %v", err))
				return
			}
		}
	}

	// 2. Build TriggerParams from route definition
	params := model.TriggerParams{
		TriggerType: model.TriggerHTTP,
		TriggerMeta: map[string]any{"path": route.Path, "method": route.Method},
		InputValues: inputValues,
	}
	if len(route.RunJobs) > 0 {
		params.RunJobs = route.RunJobs
	}
	if route.Inputs != nil {
		params.Inputs = route.Inputs
	}
	if route.Outputs != nil {
		params.Outputs = route.Outputs
	}

	// 3. Resolve RunRequest
	req, err := d.resolver.ResolveRunRequest(c.Request.Context(), d.wf, d.wfSource, params)
	if err != nil {
		respondTrigger(c, http.StatusBadRequest, "", "", nil, "", fmt.Sprintf("resolve run request: %v", err))
		return
	}
	req.WorkDir = d.workDir

	release := func() {}
	if route.Concurrency != nil {
		ticket, rel, err := d.concurrency.Acquire(c.Request.Context(), route, req)
		if err != nil {
			status := http.StatusInternalServerError
			switch {
			case errors.Is(err, errConcurrencyBusy), errors.Is(err, errConcurrencyReplaced):
				status = CodeToHTTPStatus(reederrors.CodeConflict)
			case errors.Is(err, context.Canceled):
				status = CodeToHTTPStatus(reederrors.CodeCanceled)
			case errors.Is(err, context.DeadlineExceeded):
				status = CodeToHTTPStatus(reederrors.CodeTimeout)
			}
			respondTrigger(c, status, "", "", nil, "", err.Error())
			return
		}
		release = rel
		if req.TriggerMeta == nil {
			req.TriggerMeta = make(map[string]any, 2)
		}
		req.TriggerMeta["concurrency_group"] = ticket.Group
		req.TriggerMeta["concurrency_behavior"] = ticket.Behavior
	}

	// 4. Submit Run
	result, err := d.submitter.Submit(req)
	if err != nil {
		release()
		respondTrigger(c, http.StatusInternalServerError, "", "", nil, "", fmt.Sprintf("submit run: %v", err))
		return
	}

	// 5. Async mode: route config OR request query param ?async=true
	if route.Async || cast.ToBool(c.Query("async")) {
		c.Header("Location", fmt.Sprintf("/v1/runs/%s", result.ID()))
		respondTrigger(c, http.StatusAccepted, result.ID(), "CREATED", nil, "", "")
		// Release concurrency slot asynchronously after run completes.
		// Mirror the sync-path logic: if the first WaitRun times out the run
		// may still be active, so try once more before releasing the ticket.
		go func() {
			waitCtx, waitCancel := context.WithTimeout(context.Background(), 30*time.Minute)
			_, err := d.submitter.WaitRun(waitCtx, result.ID())
			waitCancel()
			if err != nil && errors.Is(err, context.DeadlineExceeded) {
				bgCtx, bgCancel := context.WithTimeout(context.Background(), 30*time.Minute)
				_, _ = d.submitter.WaitRun(bgCtx, result.ID())
				bgCancel()
			}
			release()
		}()
		return
	}

	// 6. Sync mode: wait for completion.
	// Use a long timeout (not request ctx) so the concurrency slot stays held
	// until the run reaches terminal state, even if the HTTP client disconnects.
	// 30-minute safety cap prevents permanent goroutine leaks if engine hangs.
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 30*time.Minute)
	view, err := d.submitter.WaitRun(waitCtx, result.ID())
	waitCancel()
	if err != nil {
		// If WaitRun timed out, the run may still be active — release the ticket
		// asynchronously when it eventually completes to preserve concurrency semantics.
		if errors.Is(err, context.DeadlineExceeded) {
			go func() {
				bgCtx, bgCancel := context.WithTimeout(context.Background(), 30*time.Minute)
				_, _ = d.submitter.WaitRun(bgCtx, result.ID())
				bgCancel()
				release()
			}()
		} else {
			release()
		}
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, context.Canceled):
			status = CodeToHTTPStatus(reederrors.CodeCanceled)
		case errors.Is(err, context.DeadlineExceeded):
			status = CodeToHTTPStatus(reederrors.CodeTimeout)
		}
		respondTrigger(c, status, result.ID(), "", nil, "", fmt.Sprintf("run failed: %v", err))
		return
	}
	release() // release concurrency slot only after run completes

	// 7. Return unified response
	var errCode, msg string
	if view.Status != model.RunSucceeded {
		errCode = view.ErrorCode
		msg = view.ErrorMessage
	}
	respondTrigger(c, http.StatusOK, view.ID, string(view.Status), view.Outputs, errCode, msg)
}

// parseMultipartInputs parses a multipart/form-data request into input values.
// Text fields become string values. File fields are uploaded to media and become media:// URIs.
// Multiple files with the same name become []string.
func (d *HTTPTriggerDispatcher) parseMultipartInputs(c *gin.Context) (map[string]any, error) {
	if err := c.Request.ParseMultipartForm(32 << 20); err != nil { // 32 MiB max memory
		return nil, fmt.Errorf("parse multipart form: %w", err)
	}
	// Ensure multipart temp files are cleaned up when request completes.
	defer c.Request.MultipartForm.RemoveAll()

	result := make(map[string]any)

	// Text fields
	for key, values := range c.Request.MultipartForm.Value {
		if len(values) == 1 {
			result[key] = values[0]
		} else {
			result[key] = values
		}
	}

	// File fields → upload to media service
	if d.media == nil {
		// No media service — skip files
		return result, nil
	}
	for key, files := range c.Request.MultipartForm.File {
		var uris []string
		for _, fh := range files {
			f, err := fh.Open()
			if err != nil {
				return nil, fmt.Errorf("open uploaded file %q: %w", fh.Filename, err)
			}
			mimeType := fh.Header.Get("Content-Type")
			if mimeType == "" {
				mimeType = "application/octet-stream"
			}
			entry, err := d.media.Upload(c.Request.Context(), f, mimeType)
			f.Close()
			if err != nil {
				return nil, fmt.Errorf("upload file %q: %w", fh.Filename, err)
			}
			uris = append(uris, media.URI(entry.ID))
		}
		if len(uris) == 1 {
			result[key] = uris[0]
		} else {
			result[key] = uris
		}
	}
	return result, nil
}
