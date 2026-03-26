package http

import (
	"context"
	"net"

	"github.com/gin-gonic/gin"
)

type ipcCtxKey struct{}

// ipcConnContext injects an IPC flag into the request context.
// Used as ConnContext on the UDS http.Server.
func ipcConnContext(ctx context.Context, _ net.Conn) context.Context {
	return context.WithValue(ctx, ipcCtxKey{}, true)
}

// IsIPC returns true if the request arrived via the Unix socket listener.
func IsIPC(c *gin.Context) bool {
	v, _ := c.Request.Context().Value(ipcCtxKey{}).(bool)
	return v
}
