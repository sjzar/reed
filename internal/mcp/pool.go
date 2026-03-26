// Package mcp provides MCP (Model Context Protocol) client pool management.
// It manages connections to external MCP servers, providing tool listing,
// tool invocation, health checking, and automatic reconnection.
package mcp

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sjzar/reed/internal/model"
)

const (
	defaultMaxResultSize = 128 * 1024 // 128KB
	initTimeout          = 30 * time.Second

	// Reconnect backoff parameters.
	reconnectInitialDelay   = 100 * time.Millisecond
	reconnectMaxDelay       = 10 * time.Second
	reconnectMaxRetries     = 5
	reconnectBackoffMult    = 3.0
	reconnectAttemptTimeout = 10 * time.Second
)

// Transport abstracts the MCP client connection.
// This allows testing without the real go-sdk dependency.
type Transport interface {
	// Connect establishes the connection and performs the initialize handshake.
	Connect(ctx context.Context) error
	// ListTools returns the tools exposed by the server.
	ListTools(ctx context.Context) ([]ToolInfo, error)
	// CallTool invokes a tool on the server.
	CallTool(ctx context.Context, name string, args map[string]any) (*model.ToolResult, error)
	// Ping checks if the connection is alive.
	Ping(ctx context.Context) error
	// Close shuts down the connection.
	Close() error
}

// TransportFactory creates a Transport from an MCPServerSpec.
type TransportFactory func(spec model.MCPServerSpec) (Transport, error)

// ToolInfo describes a tool exposed by an MCP server.
type ToolInfo struct {
	Name        string
	Description string
	InputSchema map[string]any
}

// ServerInfo describes the status of an MCP server.
type ServerInfo struct {
	ID     string       `json:"id"`
	Status ServerStatus `json:"status"`
	Tools  []ToolInfo   `json:"tools"`
	Error  string       `json:"error,omitempty"`
}

// Pool manages a set of MCP server connections.
// Owned by reed.Manager as a global singleton, shared across Jobs/Steps.
// Concurrency-safe: multiple Workers can call the same server simultaneously.
type Pool struct {
	mu            sync.RWMutex
	servers       map[string]*serverEntry
	maxResultSize int
	factory       TransportFactory
	initialized   bool // guards against duplicate LoadAndInit calls

	initDone    chan struct{}      // closed when background init completes
	closeCtx    context.Context    // canceled by StopAll; gates reconnect
	closeCancel context.CancelFunc // called by StopAll
}

// PoolOption configures a Pool.
type PoolOption func(*Pool)

// WithMaxResultSize sets the maximum tool result size in bytes.
func WithMaxResultSize(bytes int) PoolOption {
	return func(p *Pool) { p.maxResultSize = bytes }
}

// WithTransportFactory sets a custom transport factory (for testing).
func WithTransportFactory(f TransportFactory) PoolOption {
	return func(p *Pool) { p.factory = f }
}

// NewPool creates a new MCP connection pool.
func NewPool(opts ...PoolOption) *Pool {
	closeCtx, closeCancel := context.WithCancel(context.Background())
	p := &Pool{
		servers:       make(map[string]*serverEntry),
		maxResultSize: defaultMaxResultSize,
		initDone:      make(chan struct{}),
		closeCtx:      closeCtx,
		closeCancel:   closeCancel,
	}
	for _, o := range opts {
		o(p)
	}
	// If no LoadAndInit is called, mark as ready immediately
	close(p.initDone)
	return p
}

// LoadAndInit registers servers and starts background concurrent initialization.
// Non-blocking: returns immediately. Use waitReady internally to block until done.
// Safe to call only once; subsequent calls log a warning and return.
func (p *Pool) LoadAndInit(ctx context.Context, specs map[string]model.MCPServerSpec) {
	if len(specs) == 0 {
		return
	}

	p.mu.Lock()
	if p.initialized {
		p.mu.Unlock()
		return
	}
	p.initialized = true

	// Re-create initDone channel (NewPool already closed it)
	p.initDone = make(chan struct{})

	// Register all servers as pending (still under p.mu.Lock from above)
	for id, spec := range specs {
		p.servers[id] = &serverEntry{
			id:     id,
			spec:   spec,
			status: ServerPending,
		}
	}
	p.mu.Unlock()

	// Start background concurrent initialization.
	// Capture initDone before launching goroutine so a second LoadAndInit call
	// (which replaces p.initDone) won't cause the first goroutine to close the
	// wrong channel.
	initCh := p.initDone
	go func() {
		defer close(initCh)

		initCtx, cancel := context.WithTimeout(ctx, initTimeout)
		defer cancel()

		var wg sync.WaitGroup
		for id := range specs {
			wg.Add(1)
			go func(serverID string) {
				defer wg.Done()
				p.initServer(initCtx, serverID)
			}(id)
		}
		wg.Wait()
	}()
}

// StartAll starts connections to all specified MCP servers synchronously.
// Kept for backward compatibility. Prefer LoadAndInit for async initialization.
func (p *Pool) StartAll(ctx context.Context, specs map[string]model.MCPServerSpec) error {
	var firstErr error
	for id, spec := range specs {
		if err := p.Start(ctx, id, spec); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Start starts a single MCP server connection synchronously.
func (p *Pool) Start(ctx context.Context, id string, spec model.MCPServerSpec) error {
	if p.factory == nil {
		return fmt.Errorf("mcp: no transport factory configured")
	}

	entry := &serverEntry{
		id:     id,
		spec:   spec,
		status: ServerStarting,
	}

	transport, err := p.factory(spec)
	if err != nil {
		entry.status = ServerFailed
		entry.err = err
		p.mu.Lock()
		p.servers[id] = entry
		p.mu.Unlock()
		return fmt.Errorf("mcp create transport %s: %w", id, err)
	}

	if err := transport.Connect(ctx); err != nil {
		entry.status = ServerFailed
		entry.err = err
		p.mu.Lock()
		p.servers[id] = entry
		p.mu.Unlock()
		return fmt.Errorf("mcp connect %s: %w", id, err)
	}

	tools, err := transport.ListTools(ctx)
	if err != nil {
		entry.status = ServerFailed
		entry.err = err
		transport.Close()
		p.mu.Lock()
		p.servers[id] = entry
		p.mu.Unlock()
		return fmt.Errorf("mcp list tools %s: %w", id, err)
	}

	entry.transport = transport
	entry.tools = tools
	entry.status = ServerReady

	p.mu.Lock()
	p.servers[id] = entry
	p.mu.Unlock()
	return nil
}

// initServer initializes a single server (called from background goroutine).
func (p *Pool) initServer(ctx context.Context, id string) {
	p.mu.RLock()
	entry, ok := p.servers[id]
	p.mu.RUnlock()
	if !ok {
		return
	}

	if p.factory == nil {
		entry.mu.Lock()
		entry.status = ServerFailed
		entry.err = fmt.Errorf("no transport factory")
		entry.mu.Unlock()
		return
	}

	entry.mu.Lock()
	entry.status = ServerStarting
	entry.mu.Unlock()

	transport, err := p.factory(entry.spec)
	if err != nil {
		entry.mu.Lock()
		entry.status = ServerFailed
		entry.err = err
		entry.mu.Unlock()
		return
	}

	if err := transport.Connect(ctx); err != nil {
		entry.mu.Lock()
		entry.status = ServerFailed
		entry.err = err
		entry.mu.Unlock()
		return
	}

	tools, err := transport.ListTools(ctx)
	if err != nil {
		entry.mu.Lock()
		entry.status = ServerFailed
		entry.err = err
		entry.mu.Unlock()
		transport.Close()
		return
	}

	entry.mu.Lock()
	if p.closeCtx.Err() != nil {
		entry.mu.Unlock()
		transport.Close()
		return
	}
	entry.transport = transport
	entry.tools = tools
	entry.status = ServerReady
	entry.err = nil
	entry.mu.Unlock()
}

// waitReady blocks until background initialization completes or ctx is canceled.
func (p *Pool) waitReady(ctx context.Context) error {
	p.mu.RLock()
	ch := p.initDone
	p.mu.RUnlock()
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Stop closes a specific MCP server connection.
func (p *Pool) Stop(id string) error {
	p.mu.Lock()
	entry, ok := p.servers[id]
	if !ok {
		p.mu.Unlock()
		return fmt.Errorf("mcp: server %s not found", id)
	}
	p.mu.Unlock()

	entry.mu.Lock()
	defer entry.mu.Unlock()
	return entry.close()
}

// StopAll closes all connections (called during teardown).
// Also cancels the pool context to abort any in-flight reconnect attempts.
func (p *Pool) StopAll() {
	p.closeCancel()

	p.mu.Lock()
	entries := make([]*serverEntry, 0, len(p.servers))
	for _, e := range p.servers {
		entries = append(entries, e)
	}
	p.mu.Unlock()

	for _, e := range entries {
		e.mu.Lock()
		e.close()
		e.mu.Unlock()
	}
}

// ListTools returns tool definitions for a specific server, converted to model.ToolDef.
func (p *Pool) ListTools(ctx context.Context, id string) ([]model.ToolDef, error) {
	if err := p.waitReady(ctx); err != nil {
		return nil, err
	}

	p.mu.RLock()
	entry, ok := p.servers[id]
	p.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("mcp: server %s not found", id)
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()

	if entry.status != ServerReady {
		return nil, fmt.Errorf("mcp: server %s not ready (status: %s)", id, entry.status)
	}

	return convertTools(entry.tools, id, false), nil
}

// ListAllTools returns tool definitions from all ready servers.
// Tool names are always prefixed: "{server_id}__{tool_name}".
func (p *Pool) ListAllTools(ctx context.Context) ([]model.ToolDef, error) {
	if err := p.waitReady(ctx); err != nil {
		return nil, err
	}

	p.mu.RLock()
	entries := make(map[string]*serverEntry, len(p.servers))
	for id, e := range p.servers {
		entries[id] = e
	}
	p.mu.RUnlock()

	var allTools []model.ToolDef

	for id, entry := range entries {
		entry.mu.Lock()
		if entry.status == ServerReady {
			allTools = append(allTools, convertTools(entry.tools, id, true)...)
		}
		entry.mu.Unlock()
	}
	return allTools, nil
}

// ListServers returns server info. If name is empty, returns all servers.
// If name is non-empty, tries exact match first, then fuzzy match.
func (p *Pool) ListServers(ctx context.Context, name string) ([]ServerInfo, error) {
	if err := p.waitReady(ctx); err != nil {
		return nil, err
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	if name == "" {
		infos := make([]ServerInfo, 0, len(p.servers))
		for _, e := range p.servers {
			infos = append(infos, p.serverInfo(e))
		}
		return infos, nil
	}

	// Exact match
	if e, ok := p.servers[name]; ok {
		return []ServerInfo{p.serverInfo(e)}, nil
	}

	// Fuzzy match
	candidates := make([]string, 0, len(p.servers))
	for id := range p.servers {
		candidates = append(candidates, id)
	}
	if m := matchName(name, candidates); m != nil {
		if e, ok := p.servers[m.Name]; ok {
			return []ServerInfo{p.serverInfo(e)}, nil
		}
	}

	return nil, fmt.Errorf("mcp: server %q not found", name)
}

func (p *Pool) serverInfo(e *serverEntry) ServerInfo {
	e.mu.Lock()
	defer e.mu.Unlock()
	info := ServerInfo{
		ID:     e.id,
		Status: e.status,
		Tools:  e.tools,
	}
	if e.err != nil {
		info.Error = e.err.Error()
	}
	return info
}

// CallTool invokes a tool on a specific server.
// Includes content protection: results exceeding maxResultSize are truncated.
func (p *Pool) CallTool(ctx context.Context, serverID, toolName string, args map[string]any) (*model.ToolResult, error) {
	if err := p.waitReady(ctx); err != nil {
		return nil, err
	}

	p.mu.RLock()
	entry, ok := p.servers[serverID]
	p.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("mcp: server %s not found", serverID)
	}

	entry.mu.Lock()
	if entry.status != ServerReady {
		entry.mu.Unlock()
		return nil, fmt.Errorf("mcp: server %s not ready (status: %s)", serverID, entry.status)
	}
	transport := entry.transport
	entry.mu.Unlock()

	result, err := transport.CallTool(ctx, toolName, args)
	if err != nil {
		// Check if reconnection is needed
		if isConnectionError(err) {
			go p.reconnect(serverID)
		}
		return nil, err
	}

	// Content protection: truncate oversized results
	if p.maxResultSize > 0 {
		totalSize := result.ContentSize()
		if totalSize > p.maxResultSize {
			result.Truncate(p.maxResultSize)
			result.Content = append(result.Content, model.Content{
				Type: "text",
				Text: fmt.Sprintf("[WARNING: result truncated from %d to %d bytes]",
					totalSize, p.maxResultSize),
			})
		}
	}

	return result, nil
}

// ServerStatus returns the connection status of a specific server.
func (p *Pool) ServerStatus(id string) ServerStatus {
	p.mu.RLock()
	entry, ok := p.servers[id]
	p.mu.RUnlock()
	if !ok {
		return ServerStopped
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	return entry.status
}

// reconnect attempts to reconnect a failed server with exponential backoff.
// Uses a CAS guard to prevent concurrent reconnect goroutines.
func (p *Pool) reconnect(id string) {
	p.mu.RLock()
	entry, ok := p.servers[id]
	p.mu.RUnlock()
	if !ok {
		return
	}

	// CAS guard: only one reconnect at a time per server
	entry.mu.Lock()
	if entry.reconnecting {
		entry.mu.Unlock()
		return
	}
	entry.reconnecting = true
	// Close old connection while holding the lock
	entry.close()
	spec := entry.spec
	entry.mu.Unlock()

	defer func() {
		entry.mu.Lock()
		entry.reconnecting = false
		entry.mu.Unlock()
	}()

	if p.factory == nil {
		entry.mu.Lock()
		entry.status = ServerFailed
		entry.err = fmt.Errorf("no transport factory")
		entry.mu.Unlock()
		return
	}

	delay := reconnectInitialDelay
	for attempt := 0; attempt < reconnectMaxRetries; attempt++ {
		if attempt > 0 {
			log.Warn().Str("server", id).Int("attempt", attempt+1).Dur("delay", delay).Msg("MCP reconnect retry")
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-p.closeCtx.Done():
				timer.Stop()
				return
			}
			delay = time.Duration(math.Min(float64(delay)*reconnectBackoffMult, float64(reconnectMaxDelay)))
		}

		// Abort if pool is shutting down.
		if p.closeCtx.Err() != nil {
			return
		}

		transport, err := p.factory(spec)
		if err != nil {
			log.Warn().Err(err).Str("server", id).Int("attempt", attempt+1).Msg("MCP reconnect: factory failed")
			continue
		}

		ctx, cancel := context.WithTimeout(p.closeCtx, reconnectAttemptTimeout)
		connectErr := transport.Connect(ctx)
		if connectErr != nil {
			cancel()
			transport.Close()
			log.Warn().Err(connectErr).Str("server", id).Int("attempt", attempt+1).Msg("MCP reconnect: connect failed")
			continue
		}

		tools, listErr := transport.ListTools(ctx)
		cancel()
		if listErr != nil {
			transport.Close()
			log.Warn().Err(listErr).Str("server", id).Int("attempt", attempt+1).Msg("MCP reconnect: list tools failed")
			continue
		}

		// Success
		entry.mu.Lock()
		if p.closeCtx.Err() != nil {
			entry.mu.Unlock()
			transport.Close()
			return
		}
		entry.transport = transport
		entry.tools = tools
		entry.status = ServerReady
		entry.err = nil
		entry.mu.Unlock()
		log.Info().Str("server", id).Int("attempts", attempt+1).Msg("MCP reconnect succeeded")
		return
	}

	// All retries exhausted
	entry.mu.Lock()
	entry.status = ServerFailed
	entry.err = fmt.Errorf("reconnect failed after %d attempts", reconnectMaxRetries)
	entry.mu.Unlock()
	log.Error().Str("server", id).Int("maxRetries", reconnectMaxRetries).Msg("MCP reconnect exhausted")
}

// isConnectionError checks if an error indicates a broken connection.
func isConnectionError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "EOF")
}
