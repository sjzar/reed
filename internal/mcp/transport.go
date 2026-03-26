package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"

	gosdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sjzar/reed/internal/model"
)

// sdkTransport wraps the go-sdk Client/ClientSession to implement Transport.
type sdkTransport struct {
	client      *gosdk.Client
	session     *gosdk.ClientSession
	mkTransport func() gosdk.Transport
}

// Connect establishes the MCP connection and performs the initialize handshake.
func (t *sdkTransport) Connect(ctx context.Context) error {
	transport := t.mkTransport()
	session, err := t.client.Connect(ctx, transport, nil)
	if err != nil {
		return classifyError(err)
	}
	t.session = session
	return nil
}

// ListTools returns all tools exposed by the server, handling pagination.
func (t *sdkTransport) ListTools(ctx context.Context) ([]ToolInfo, error) {
	var tools []ToolInfo
	for tool, err := range t.session.Tools(ctx, nil) {
		if err != nil {
			return tools, classifyError(err)
		}
		tools = append(tools, convertSDKTool(tool))
	}
	return tools, nil
}

// CallTool invokes a tool on the server.
func (t *sdkTransport) CallTool(ctx context.Context, name string, args map[string]any) (*model.ToolResult, error) {
	result, err := t.session.CallTool(ctx, &gosdk.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		return nil, classifyError(err)
	}
	return convertSDKResult(result), nil
}

// Ping checks if the connection is alive.
func (t *sdkTransport) Ping(ctx context.Context) error {
	return classifyError(t.session.Ping(ctx, nil))
}

// Close shuts down the connection.
func (t *sdkTransport) Close() error {
	if t.session != nil {
		return t.session.Close()
	}
	return nil
}

// NewTransportFactory returns a TransportFactory that creates real MCP transports
// using the go-sdk, dispatching on MCPServerSpec.Transport.
func NewTransportFactory() TransportFactory {
	return func(spec model.MCPServerSpec) (Transport, error) {
		client := gosdk.NewClient(&gosdk.Implementation{
			Name:    "reed",
			Version: "0.1.0",
		}, nil)

		mkTransport, err := makeSDKTransport(spec)
		if err != nil {
			return nil, err
		}
		return &sdkTransport{
			client:      client,
			mkTransport: mkTransport,
		}, nil
	}
}

// makeSDKTransport returns a closure that creates the appropriate go-sdk Transport.
func makeSDKTransport(spec model.MCPServerSpec) (func() gosdk.Transport, error) {
	switch strings.ToLower(spec.Transport) {
	case "stdio", "":
		if spec.Command == "" {
			return nil, fmt.Errorf("mcp: stdio transport requires command")
		}
		return func() gosdk.Transport {
			cmd := exec.Command(spec.Command, spec.Args...)
			cmd.Env = buildEnv(spec.Env)
			cmd.Stderr = os.Stderr
			return &gosdk.CommandTransport{Command: cmd}
		}, nil

	case "sse":
		if spec.URL == "" {
			return nil, fmt.Errorf("mcp: sse transport requires url")
		}
		return func() gosdk.Transport {
			return &gosdk.SSEClientTransport{
				Endpoint:   spec.URL,
				HTTPClient: headerClient(spec.Header),
			}
		}, nil

	case "streamable-http":
		if spec.URL == "" {
			return nil, fmt.Errorf("mcp: streamable-http transport requires url")
		}
		return func() gosdk.Transport {
			return &gosdk.StreamableClientTransport{
				Endpoint:   spec.URL,
				HTTPClient: headerClient(spec.Header),
			}
		}, nil

	default:
		return nil, fmt.Errorf("mcp: unsupported transport type: %q", spec.Transport)
	}
}

// convertSDKTool converts a go-sdk Tool to our ToolInfo.
// InputSchema is converted via JSON round-trip since the SDK uses any.
func convertSDKTool(t *gosdk.Tool) ToolInfo {
	info := ToolInfo{
		Name:        t.Name,
		Description: t.Description,
	}
	if t.InputSchema != nil {
		data, err := json.Marshal(t.InputSchema)
		if err == nil {
			var schema map[string]any
			if json.Unmarshal(data, &schema) == nil {
				info.InputSchema = schema
			}
		}
	}
	return info
}

// convertSDKResult converts a go-sdk CallToolResult to model.ToolResult.
func convertSDKResult(r *gosdk.CallToolResult) *model.ToolResult {
	result := &model.ToolResult{
		IsError: r.IsError,
	}
	for _, c := range r.Content {
		switch v := c.(type) {
		case *gosdk.TextContent:
			result.Content = append(result.Content, model.Content{
				Type: "text",
				Text: v.Text,
			})
		case *gosdk.ImageContent:
			result.Content = append(result.Content, model.Content{
				Type:     model.ContentTypeImage,
				MIMEType: v.MIMEType,
				MediaURI: "data:" + v.MIMEType + ";base64," + string(v.Data),
			})
		default:
			// Other content types (audio, resource, etc.) — store as text fallback
			data, err := json.Marshal(c)
			if err == nil {
				result.Content = append(result.Content, model.Content{
					Type: "text",
					Text: string(data),
				})
			}
		}
	}
	return result
}

// classifyError wraps an error into a ConnError with the appropriate kind.
func classifyError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	kind := ConnErrOther

	switch {
	case errors.Is(err, gosdk.ErrConnectionClosed):
		kind = ConnErrOffline
	case strings.Contains(msg, "exit status"):
		kind = ConnErrStdioExit
	case strings.Contains(msg, "broken pipe"), strings.Contains(msg, "connection refused"),
		strings.Contains(msg, "connection reset"), strings.Contains(msg, "EOF"):
		kind = ConnErrOffline
	case strings.Contains(msg, "401"), strings.Contains(msg, "403"),
		strings.Contains(msg, "Unauthorized"), strings.Contains(msg, "Forbidden"):
		kind = ConnErrAuth
	}

	return &ConnError{
		Kind:    kind,
		Message: msg,
		Cause:   err,
	}
}

// buildEnv creates an environment slice inheriting os.Environ with overlay applied.
func buildEnv(overlay map[string]string) []string {
	if len(overlay) == 0 {
		return nil
	}
	env := os.Environ()
	for k, v := range overlay {
		env = append(env, k+"="+v)
	}
	return env
}

// headerClient returns an *http.Client that injects the given headers into every request.
// Returns nil if headers is empty (go-sdk uses http.DefaultClient for nil).
func headerClient(headers map[string]string) *http.Client {
	if len(headers) == 0 {
		return nil
	}
	return &http.Client{
		Transport: &headerRoundTripper{
			base:    http.DefaultTransport,
			headers: headers,
		},
	}
}

type headerRoundTripper struct {
	base    http.RoundTripper
	headers map[string]string
}

func (rt *headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	for k, v := range rt.headers {
		clone.Header.Set(k, v)
	}
	return rt.base.RoundTrip(clone)
}
