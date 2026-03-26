package http

import (
	"context"
	"net"
	"net/http"
	"time"
)

// Dial creates an HTTP client that connects via the given Unix socket path.
func Dial(sockPath string) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.DialTimeout("unix", sockPath, 2*time.Second)
			},
		},
		Timeout: 5 * time.Second,
	}
}
