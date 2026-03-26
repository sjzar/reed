package workflow

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"strings"

	"github.com/sjzar/reed/internal/model"
	"github.com/sjzar/reed/internal/skill"
	"github.com/sjzar/reed/pkg/configpatch"
)

// Service implements reed.RunResolver.
// All methods delegate to package-level functions.
type Service struct{}

// NewService creates a new workflow Service.
func NewService() *Service { return &Service{} }

// ResolveRunRequest implements RunResolver.
func (s *Service) ResolveRunRequest(ctx context.Context, wf *model.Workflow, wfSource string, params model.TriggerParams) (*model.RunRequest, error) {
	return ResolveRunRequest(ctx, wf, wfSource, params)
}

// LoadAndResolve loads a base workflow, applies set-file patches, --set and
// --set-string flags, then validates the result. Returns a fully typed *model.Workflow.
//
// Pipeline: LoadBase -> set-file RFC 7386 merge -> ApplySet -> ApplySetString -> ParseRaw -> Validate
func LoadAndResolve(base string, setFiles, sets, setStrings []string) (*model.Workflow, error) {
	raw, err := LoadBase(base)
	if err != nil {
		return nil, err
	}

	// No patches -> early validate and return
	if len(setFiles) == 0 && len(sets) == 0 && len(setStrings) == 0 {
		wf, err := ParseRaw(raw)
		if err != nil {
			return nil, err
		}
		wf.Source = base
		if err := Validate(wf); err != nil {
			return nil, err
		}
		return wf, nil
	}

	// Apply set-file patches (RFC 7386 merge, in order)
	for _, f := range setFiles {
		patch, err := LoadSetFile(f)
		if err != nil {
			return nil, err
		}
		raw = configpatch.MergeRFC7386(raw, patch)
	}

	// Apply --set (type-inferred path assignment)
	if len(sets) > 0 {
		raw, err = configpatch.ApplySet(raw, sets)
		if err != nil {
			return nil, fmt.Errorf("--set: %w", err)
		}
	}

	// Apply --set-string (forced string path assignment)
	if len(setStrings) > 0 {
		raw, err = configpatch.ApplySetString(raw, setStrings)
		if err != nil {
			return nil, fmt.Errorf("--set-string: %w", err)
		}
	}

	wf, err := ParseRaw(raw)
	if err != nil {
		return nil, err
	}
	wf.Source = base
	if err := Validate(wf); err != nil {
		return nil, err
	}
	return wf, nil
}

// ValidateFile loads and validates a workflow file without resolving patches.
func ValidateFile(path string) error {
	raw, err := LoadBase(path)
	if err != nil {
		return err
	}
	wf, err := ParseRaw(raw)
	if err != nil {
		return err
	}
	if err := Validate(wf); err != nil {
		return err
	}
	// Lightweight skill path validation
	workflowDir := filepath.Dir(path)
	return skill.ValidateWorkflowSkills(wf.Skills, workflowDir)
}

// PrepareWorkflow executes the full workflow preparation pipeline:
// load -> merge set-files -> apply sets/set-strings/envs -> validate.
func PrepareWorkflow(base string, setFiles, sets, setStrings, envs []string) (*model.Workflow, error) {
	// Merge --env into --set as "env.K=V"
	merged := make([]string, len(sets))
	copy(merged, sets)
	for _, e := range envs {
		merged = append(merged, "env."+e)
	}

	wf, err := LoadAndResolve(base, setFiles, merged, setStrings)
	if err != nil {
		return nil, fmt.Errorf("load workflow: %w", err)
	}

	return wf, nil
}

// ResolveRunRequest builds a unified RunRequest from a prepared Workflow and trigger params.
// It handles: run_jobs subgraph filtering, inputs resolution, outputs override, and env merge.
func ResolveRunRequest(ctx context.Context, wf *model.Workflow, wfSource string, params model.TriggerParams) (*model.RunRequest, error) {
	// 1. Effective run_jobs: trigger -> workflow -> nil (all)
	effectiveRunJobs := params.RunJobs
	if len(effectiveRunJobs) == 0 {
		effectiveRunJobs = wf.RunJobs
	}

	// 2. Validate run_jobs roots exist (actual filtering done by engine at Submit)
	if len(effectiveRunJobs) > 0 {
		for _, r := range effectiveRunJobs {
			if _, ok := wf.Jobs[r]; !ok {
				return nil, fmt.Errorf("subgraph filter: root job %q not found", r)
			}
		}
	}

	// 3. Effective inputs spec: trigger (non-nil = full override) -> workflow
	inputSpecs := wf.Inputs
	if params.Inputs != nil {
		inputSpecs = params.Inputs
	}

	// 4. Resolve input values: trigger values -> spec defaults; validate required
	inputs := make(map[string]any, len(inputSpecs))
	for id, spec := range inputSpecs {
		if v, ok := params.InputValues[id]; ok {
			inputs[id] = v
		} else if spec.Default != nil {
			inputs[id] = spec.Default
		} else if spec.Required {
			return nil, fmt.Errorf("required input %q not provided", id)
		}
	}

	// 4b. Normalize media inputs: enforce cardinality
	for id, spec := range inputSpecs {
		v, ok := inputs[id]
		if !ok {
			continue
		}
		switch spec.Type {
		case "media":
			// Single media: ensure string
			if s, ok := v.(string); ok {
				inputs[id] = s
			} else {
				return nil, fmt.Errorf("input %q: media type requires a single string value", id)
			}
		case "[]media":
			// Multi media: ensure []string
			switch val := v.(type) {
			case []string:
				inputs[id] = val
			case []any:
				strs := make([]string, 0, len(val))
				for _, item := range val {
					s, ok := item.(string)
					if !ok {
						return nil, fmt.Errorf("input %q: []media requires string values", id)
					}
					strs = append(strs, s)
				}
				inputs[id] = strs
			case string:
				// Single value -> wrap in slice
				inputs[id] = []string{val}
			default:
				return nil, fmt.Errorf("input %q: []media type requires string or []string value", id)
			}
		}
	}

	// 5. Effective outputs: trigger (non-nil = full override) -> workflow
	outputs := wf.Outputs
	if params.Outputs != nil {
		outputs = params.Outputs
	}

	// 6. Merge env: workflow base + trigger overlay
	env := make(map[string]string, len(wf.Env)+len(params.Env))
	maps.Copy(env, wf.Env)
	maps.Copy(env, params.Env)

	return &model.RunRequest{
		Workflow:       wf,
		WorkflowSource: wfSource,
		TriggerType:    params.TriggerType,
		TriggerMeta:    params.TriggerMeta,
		RunJobs:        effectiveRunJobs,
		Inputs:         inputs,
		Outputs:        outputs,
		Env:            env,
	}, nil
}

// ParseCLIEnvs converts --env K=V flags into a map.
func ParseCLIEnvs(envs []string) map[string]string {
	result := make(map[string]string, len(envs))
	for _, e := range envs {
		if k, v, ok := strings.Cut(e, "="); ok {
			result[k] = v
		}
	}
	return result
}
