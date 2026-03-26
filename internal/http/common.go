package http

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Respond sends data as a JSON envelope.
// IPC connections are tagged with X-Reed-IPC header for future differentiation.
func (s *Service) Respond(c *gin.Context, data any) {
	if IsIPC(c) {
		c.Header("X-Reed-IPC", "true")
	}
	c.JSON(http.StatusOK, gin.H{"code": http.StatusOK, "data": data})
}

// RespondError sends an error as a JSON envelope.
func (s *Service) RespondError(c *gin.Context, err error) {
	defer c.Abort()
	status := HTTPStatus(err)
	if IsIPC(c) {
		c.Header("X-Reed-IPC", "true")
	}
	c.JSON(status, gin.H{"code": status, "message": err.Error()})
}
