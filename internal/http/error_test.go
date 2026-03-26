package http

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	reederrors "github.com/sjzar/reed/internal/errors"
)

func TestHTTPStatus(t *testing.T) {
	tests := []struct {
		code reederrors.Code
		want int
	}{
		{reederrors.CodeNotFound, http.StatusNotFound},
		{reederrors.CodeValidation, http.StatusBadRequest},
		{reederrors.CodeInvalidArg, http.StatusBadRequest},
		{reederrors.CodeConflict, http.StatusConflict},
		{reederrors.CodeAlreadyExists, http.StatusConflict},
		{reederrors.CodePermission, http.StatusForbidden},
		{reederrors.CodeUnavailable, http.StatusServiceUnavailable},
		{reederrors.CodeTimeout, http.StatusGatewayTimeout},
		{reederrors.CodeInternal, http.StatusInternalServerError},
	}
	for _, tt := range tests {
		err := reederrors.New(tt.code, "test")
		got := HTTPStatus(err)
		if got != tt.want {
			t.Errorf("HTTPStatus(code=%q) = %d, want %d", tt.code, got, tt.want)
		}
	}
}

func TestCodeToHTTPStatusCanceled(t *testing.T) {
	got := CodeToHTTPStatus(reederrors.CodeCanceled)
	if got != 499 {
		t.Errorf("CodeToHTTPStatus(CodeCanceled) = %d, want 499", got)
	}
}

func TestHTTPStatusNil(t *testing.T) {
	got := HTTPStatus(nil)
	if got != http.StatusOK {
		t.Errorf("HTTPStatus(nil) = %d, want %d", got, http.StatusOK)
	}
}

func TestErrorHandlerMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("sets X-Request-ID header", func(t *testing.T) {
		r := gin.New()
		r.Use(ErrorHandlerMiddleware())
		r.GET("/ping", func(c *gin.Context) {
			c.Status(http.StatusOK)
		})
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/ping", nil)
		r.ServeHTTP(w, req)
		if w.Header().Get("X-Request-ID") == "" {
			t.Error("X-Request-ID header should be set")
		}
	})

	t.Run("returns error JSON for domain error", func(t *testing.T) {
		r := gin.New()
		r.Use(ErrorHandlerMiddleware())
		r.GET("/fail", func(c *gin.Context) {
			_ = c.Error(reederrors.New(reederrors.CodeNotFound, "resource missing"))
		})
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/fail", nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
		}
	})

	t.Run("returns 500 for plain error", func(t *testing.T) {
		r := gin.New()
		r.Use(ErrorHandlerMiddleware())
		r.GET("/plain", func(c *gin.Context) {
			_ = c.Error(fmt.Errorf("unexpected"))
		})
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/plain", nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
		}
	})
}

func TestRecoveryMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("recovers from error panic", func(t *testing.T) {
		r := gin.New()
		r.Use(RecoveryMiddleware())
		r.GET("/panic-err", func(c *gin.Context) {
			panic(fmt.Errorf("something exploded"))
		})
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/panic-err", nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
		}
	})

	t.Run("recovers from non-error panic", func(t *testing.T) {
		r := gin.New()
		r.Use(RecoveryMiddleware())
		r.GET("/panic-str", func(c *gin.Context) {
			panic("string panic")
		})
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/panic-str", nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
		}
	})

	t.Run("no panic passes through", func(t *testing.T) {
		r := gin.New()
		r.Use(RecoveryMiddleware())
		r.GET("/ok", func(c *gin.Context) {
			c.Status(http.StatusOK)
		})
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/ok", nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})
}
