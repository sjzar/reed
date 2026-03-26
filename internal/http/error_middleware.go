package http

import (
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	reederrors "github.com/sjzar/reed/internal/errors"
)

// ErrorHandlerMiddleware handles errors collected during request processing.
func ErrorHandlerMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := uuid.New().String()
		c.Set("RequestID", requestID)
		c.Header("X-Request-ID", requestID)

		c.Next()

		if len(c.Errors) > 0 {
			for _, e := range c.Errors {
				log.Warn().Err(e.Err).Str("requestID", requestID).Msg("request error")
			}
			if !c.Writer.Written() {
				err := c.Errors[0].Err
				status := HTTPStatus(err)
				c.JSON(status, gin.H{"message": err.Error()})
				c.Abort()
			}
		}
	}
}

// RecoveryMiddleware recovers from panics and returns a 500 response.
func RecoveryMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				var appErr *reederrors.Error
				switch v := r.(type) {
				case error:
					appErr = reederrors.Wrap(v, reederrors.CodeInternal, "panic recovered")
				default:
					appErr = reederrors.Newf(reederrors.CodeInternal, "panic recovered: %v", r)
				}
				log.Err(appErr).Msgf("PANIC RECOVERED\n%s", string(debug.Stack()))
				c.JSON(http.StatusInternalServerError, appErr)
				c.Abort()
			}
		}()
		c.Next()
	}
}
