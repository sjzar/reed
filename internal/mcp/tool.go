package mcp

import (
	"fmt"

	"github.com/sjzar/reed/internal/model"
)

// convertTools converts ToolInfo to model.ToolDef, optionally prefixing names.
func convertTools(tools []ToolInfo, serverID string, prefix bool) []model.ToolDef {
	defs := make([]model.ToolDef, len(tools))
	for i, t := range tools {
		name := t.Name
		if prefix {
			name = fmt.Sprintf("%s__%s", serverID, t.Name)
		}
		defs[i] = model.ToolDef{
			Name:        name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		}
	}
	return defs
}

// ConnError classifies MCP connection errors.
type ConnError struct {
	Kind    ConnErrorKind
	Message string
	Cause   error
}

func (e *ConnError) Error() string { return e.Message }
func (e *ConnError) Unwrap() error { return e.Cause }

// ConnErrorKind classifies the type of connection error.
type ConnErrorKind string

const (
	ConnErrStdioExit ConnErrorKind = "stdio_exit"
	ConnErrOffline   ConnErrorKind = "offline"
	ConnErrAuth      ConnErrorKind = "auth"
	ConnErrOther     ConnErrorKind = "other"
)
