package mcp

import (
	"sync"

	"github.com/sjzar/reed/internal/model"
)

// ServerStatus represents the connection state of an MCP server.
type ServerStatus string

const (
	ServerPending  ServerStatus = "pending"
	ServerStarting ServerStatus = "starting"
	ServerReady    ServerStatus = "ready"
	ServerFailed   ServerStatus = "failed"
	ServerStopped  ServerStatus = "stopped"
)

// serverEntry wraps a single MCP server connection.
type serverEntry struct {
	id           string
	spec         model.MCPServerSpec
	mu           sync.Mutex
	transport    Transport
	tools        []ToolInfo
	status       ServerStatus
	err          error
	reconnecting bool // guards against concurrent reconnect goroutines
}

// close shuts down the transport. Must be called with mu held.
func (e *serverEntry) close() error {
	if e.transport == nil {
		return nil
	}
	err := e.transport.Close()
	e.transport = nil
	e.status = ServerStopped
	return err
}
