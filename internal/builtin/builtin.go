package builtin

import (
	"embed"
	"fmt"

	"github.com/sjzar/reed/internal/model"
	"github.com/sjzar/reed/internal/workflow"
)

//go:embed workflows/*.yml
var Workflows embed.FS

// LoadWorkflow loads and parses an embedded workflow by name.
func LoadWorkflow(name string) (*model.Workflow, error) {
	data, err := Workflows.ReadFile("workflows/" + name + ".yml")
	if err != nil {
		return nil, fmt.Errorf("read embedded workflow %q: %w", name, err)
	}
	raw, err := workflow.ParseBytes(data)
	if err != nil {
		return nil, fmt.Errorf("parse embedded workflow %q: %w", name, err)
	}
	wf, err := workflow.ParseRaw(raw)
	if err != nil {
		return nil, fmt.Errorf("build embedded workflow %q: %w", name, err)
	}
	if err := workflow.Validate(wf); err != nil {
		return nil, fmt.Errorf("validate embedded workflow %q: %w", name, err)
	}
	wf.Source = "builtin://" + name
	return wf, nil
}
