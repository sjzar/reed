package workflow

import (
	"fmt"
	"strings"

	"github.com/robfig/cron/v3"
	"github.com/sjzar/reed/internal/model"
	"github.com/sjzar/reed/internal/skill"
)

// ValidationError collects multiple validation issues.
type ValidationError struct {
	Issues []string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("workflow validation failed:\n  - %s", strings.Join(e.Issues, "\n  - "))
}

func (e *ValidationError) add(format string, args ...any) {
	e.Issues = append(e.Issues, fmt.Sprintf(format, args...))
}

func (e *ValidationError) hasIssues() bool {
	return len(e.Issues) > 0
}

// Validate checks a *model.Workflow for structural correctness.
// It performs all compile-time checks from WORKFLOW-SPEC §9.
func Validate(wf *model.Workflow) error {
	ve := &ValidationError{}

	validateJobs(wf, ve)
	validateOnBlock(wf, ve)
	validateToolAccess(wf, ve)
	validateShellFields(wf, ve)
	validateAgentRefs(wf, ve)
	validateSkillRefs(wf, ve)
	validateSkillSpecs(wf, ve)
	validateMCPServers(wf, ve)
	validateOutputs(wf, ve)

	if ve.hasIssues() {
		return ve
	}
	return nil
}

func validateJobs(wf *model.Workflow, ve *ValidationError) {
	if len(wf.Jobs) == 0 {
		ve.add("missing required field: jobs")
		return
	}

	jobIDs := make(map[string]bool, len(wf.Jobs))
	for id := range wf.Jobs {
		jobIDs[id] = true
	}

	for id, job := range wf.Jobs {
		validateJobNeeds(id, job, jobIDs, ve)
		validateJobSteps(id, job, ve)
	}

	if err := detectCycles(wf.Jobs); err != nil {
		ve.add("%s", err)
	}
}

func validateJobNeeds(jobID string, job model.Job, jobIDs map[string]bool, ve *ValidationError) {
	for _, dep := range job.Needs {
		if !jobIDs[dep] {
			ve.add("job %q: needs references non-existent job %q", jobID, dep)
		}
		if dep == jobID {
			ve.add("job %q: cannot depend on itself", jobID)
		}
	}
}

func validateJobSteps(jobID string, job model.Job, ve *ValidationError) {
	if len(job.Steps) == 0 {
		ve.add("job %q: missing required field: steps", jobID)
		return
	}

	stepIDs := make(map[string]bool)
	for i, step := range job.Steps {
		validateStep(jobID, i, step, stepIDs, ve)
	}
}

func validateStep(jobID string, idx int, step model.Step, stepIDs map[string]bool, ve *ValidationError) {
	if step.ID != "" {
		if stepIDs[step.ID] {
			ve.add("job %q: duplicate step id %q", jobID, step.ID)
		}
		stepIDs[step.ID] = true
	}

	// background steps must have explicit id
	if step.Background && step.ID == "" {
		ve.add("job %q: step[%d] with background=true must have explicit id", jobID, idx)
	}

	if step.Uses != "" {
		validateUses(jobID, idx, step.Uses, ve)
	}
}

func validateUses(jobID string, idx int, uses string, ve *ValidationError) {
	if uses == "" {
		return
	}
	if isURL(uses) {
		ve.add("job %q: step[%d]: remote uses URL not allowed: %s", jobID, idx, uses)
	}
	if strings.Contains(uses, "#") {
		ve.add("job %q: step[%d]: fragment references not allowed in uses: %s", jobID, idx, uses)
	}
	if strings.Contains(uses, "@") {
		ve.add("job %q: step[%d]: registry references not allowed in uses: %s", jobID, idx, uses)
	}
}

func validateToolAccess(wf *model.Workflow, ve *ValidationError) {
	if wf.ToolAccess != "" && wf.ToolAccess != "workdir" && wf.ToolAccess != "full" {
		ve.add("tool_access: must be %q or %q, got %q", "workdir", "full", wf.ToolAccess)
	}
}

// validShellIDs is the set of known shell identifiers.
// Each requires different invocation flags, so arbitrary executables are not allowed.
var validShellIDs = map[string]bool{
	"bash": true, "sh": true, "zsh": true, "powershell": true, "cmd": true,
}

// shellWorkerUses is the set of uses values that support the shell field.
var shellWorkerUses = map[string]bool{
	"shell": true, "bash": true, "run": true, "": true,
}

func validateShellFields(wf *model.Workflow, ve *ValidationError) {
	for jobID, job := range wf.Jobs {
		for i, step := range job.Steps {
			if step.Shell == "" {
				continue
			}
			// shell field only valid on shell-type workers
			if !shellWorkerUses[step.Uses] {
				ve.add("job %q: step[%d]: shell field not allowed on uses %q", jobID, i, step.Uses)
				continue
			}
			// validate known shell ID
			if !validShellIDs[step.Shell] {
				ve.add("job %q: step[%d]: unknown shell %q (supported: bash, sh, zsh, powershell, cmd)", jobID, i, step.Shell)
				continue
			}
			// uses: bash rejects shell override unless it's "bash"
			if step.Uses == "bash" && step.Shell != "bash" {
				ve.add("job %q: step[%d]: uses: bash does not allow shell: %q", jobID, i, step.Shell)
			}
		}
	}
}

func validateOnBlock(wf *model.Workflow, ve *ValidationError) {
	hasCLI := wf.On.CLI != nil
	hasService := wf.On.Service != nil
	hasSchedule := len(wf.On.Schedule) > 0

	if hasCLI && hasService {
		ve.add("on: cli and service are mutually exclusive")
	}
	if hasCLI && hasSchedule {
		ve.add("on: cli and schedule are mutually exclusive")
	}

	// Validate workflow-level run_jobs
	validateRunJobsSlice("run_jobs", wf.RunJobs, wf, ve)

	if hasService {
		validateServiceTrigger(wf.On.Service, wf, ve)
	}

	if hasCLI {
		validateCLICommands(wf.On.CLI, wf, ve)
	}

	if hasSchedule {
		validateScheduleRules(wf.On.Schedule, wf, ve)
	}
}

func validateServiceTrigger(svc *model.ServiceTrigger, wf *model.Workflow, ve *ValidationError) {
	if svc.Port <= 0 {
		ve.add("on.service: missing or invalid required field: port")
	}

	for i, r := range svc.HTTP {
		validateRunJobsSlice(fmt.Sprintf("on.service.http[%d]", i), r.RunJobs, wf, ve)
		validateHTTPRoute(i, r, ve)
		validateTriggerInputs(fmt.Sprintf("on.service.http[%d]", i), r.Inputs, ve)
	}
	for i, t := range svc.MCP {
		validateRunJobsSlice(fmt.Sprintf("on.service.mcp[%d]", i), t.RunJobs, wf, ve)
		validateMCPTool(i, t, ve)
		validateTriggerInputs(fmt.Sprintf("on.service.mcp[%d]", i), t.Inputs, ve)
	}
}

var validHTTPMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true, "HEAD": true, "OPTIONS": true,
}

func validateHTTPRoute(idx int, route model.HTTPRoute, ve *ValidationError) {
	if route.Path == "" {
		ve.add("on.service.http[%d]: missing required field: path", idx)
	}
	if route.Method == "" {
		ve.add("on.service.http[%d]: missing required field: method", idx)
	} else if !validHTTPMethods[strings.ToUpper(route.Method)] {
		ve.add("on.service.http[%d]: invalid method %q", idx, route.Method)
	}
	validateConcurrencySpec(idx, route.Concurrency, ve)
}

var supportedHTTPConcurrencyBehaviors = map[string]bool{
	model.ConcurrencyBehaviorQueue:          true,
	model.ConcurrencyBehaviorSkip:           true,
	model.ConcurrencyBehaviorReplacePending: true,
}

func validateConcurrencySpec(idx int, spec *model.ConcurrencySpec, ve *ValidationError) {
	if spec == nil {
		return
	}
	if strings.TrimSpace(spec.Group) == "" {
		ve.add("on.service.http[%d].concurrency: missing required field: group", idx)
	}
	behavior := spec.EffectiveBehavior()
	if !supportedHTTPConcurrencyBehaviors[behavior] {
		ve.add(
			"on.service.http[%d].concurrency.behavior: unsupported value %q (supported: %s, %s, %s)",
			idx,
			behavior,
			model.ConcurrencyBehaviorQueue,
			model.ConcurrencyBehaviorSkip,
			model.ConcurrencyBehaviorReplacePending,
		)
	}
}

func validateMCPTool(idx int, tool model.MCPTool, ve *ValidationError) {
	if tool.Name == "" {
		ve.add("on.service.mcp[%d]: missing required field: name", idx)
	}
}

func validateScheduleRule(idx int, rule model.ScheduleRule, ve *ValidationError) {
	if rule.Cron == "" {
		ve.add("on.schedule[%d]: missing required field: cron", idx)
		return
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	if _, err := parser.Parse(rule.Cron); err != nil {
		ve.add("on.schedule[%d]: invalid cron expression %q: %v", idx, rule.Cron, err)
	}
}

func validateScheduleRules(rules []model.ScheduleRule, wf *model.Workflow, ve *ValidationError) {
	for i, r := range rules {
		validateRunJobsSlice(fmt.Sprintf("on.schedule[%d]", i), r.RunJobs, wf, ve)
		validateScheduleRule(i, r, ve)
	}
}

func validateCLICommands(cli *model.CLITrigger, wf *model.Workflow, ve *ValidationError) {
	for name, cmd := range cli.Commands {
		ctx := fmt.Sprintf("on.cli.commands.%s", name)
		validateRunJobsSlice(ctx, cmd.RunJobs, wf, ve)
		validateTriggerInputs(ctx, cmd.Inputs, ve)
	}
}

var validInputTypes = map[string]bool{
	"string": true, "number": true, "boolean": true, "integer": true,
	"media": true, "[]media": true,
}

func validateTriggerInputs(ctx string, inputs map[string]model.InputSpec, ve *ValidationError) {
	for key, spec := range inputs {
		if spec.Type != "" && !validInputTypes[spec.Type] {
			ve.add("%s.inputs.%s: invalid type %q", ctx, key, spec.Type)
		}
	}
}

func validateRunJobsSlice(ctx string, runJobs []string, wf *model.Workflow, ve *ValidationError) {
	for _, jobID := range runJobs {
		if _, exists := wf.Jobs[jobID]; !exists {
			ve.add("%s: run_jobs references non-existent job %q", ctx, jobID)
		}
	}
}

func validateAgentRefs(wf *model.Workflow, ve *ValidationError) {
	for jobID, job := range wf.Jobs {
		for i, step := range job.Steps {
			if step.Uses != "agent" {
				continue
			}
			agentRef, _ := step.With["agent"].(string)
			if agentRef != "" {
				if _, exists := wf.Agents[agentRef]; !exists {
					ve.add("job %q: step[%d]: with.agent references undefined agent %q", jobID, i, agentRef)
				}
			}
		}
	}
}

// validateSkillRefs checks that agent skill references are non-empty strings.
// Compile-time only — does not check if skills exist in the catalog.
func validateSkillRefs(wf *model.Workflow, ve *ValidationError) {
	for agentID, agent := range wf.Agents {
		for i, skillRef := range agent.Skills {
			if skillRef == "" {
				ve.add("agent %q: skills[%d] is empty", agentID, i)
			}
		}
	}
}

// validateSkillSpecs checks that workflow skill specs are structurally valid.
func validateSkillSpecs(wf *model.Workflow, ve *ValidationError) {
	for id, spec := range wf.Skills {
		if spec.Uses == "" && len(spec.Resources) == 0 {
			ve.add("skill %q: must have uses or resources", id)
		}
		if spec.Uses != "" && len(spec.Resources) > 0 {
			ve.add("skill %q: uses and resources are mutually exclusive", id)
		}
		if len(spec.Resources) > 0 {
			if err := skill.ValidateResources(spec.Resources); err != nil {
				ve.add("skill %q: %s", id, err)
			}
		}
	}
}

// ValidateSkillRefsResolvable checks that all agent skill references exist in the given catalog IDs.
// This is a runtime check, called after skill scanning/loading.
func ValidateSkillRefsResolvable(wf *model.Workflow, catalogIDs []string) error {
	idSet := make(map[string]bool, len(catalogIDs))
	for _, id := range catalogIDs {
		idSet[id] = true
	}
	ve := &ValidationError{}
	for agentID, agent := range wf.Agents {
		for _, skillRef := range agent.Skills {
			if !idSet[skillRef] {
				ve.add("agent %q: skill %q not found in catalog", agentID, skillRef)
			}
		}
	}
	if ve.hasIssues() {
		return ve
	}
	return nil
}

var validMCPTransports = map[string]bool{
	"stdio": true, "sse": true, "streamable-http": true,
}

func validateMCPServers(wf *model.Workflow, ve *ValidationError) {
	for id, srv := range wf.MCPServers {
		if srv.Transport == "" {
			ve.add("mcp_servers.%s: missing required field: transport", id)
			continue
		}
		if !validMCPTransports[srv.Transport] {
			ve.add("mcp_servers.%s: invalid transport %q (must be stdio, sse, or streamable-http)", id, srv.Transport)
			continue
		}
		switch srv.Transport {
		case "stdio":
			if srv.Command == "" {
				ve.add("mcp_servers.%s: stdio transport requires command", id)
			}
		case "sse", "streamable-http":
			if srv.URL == "" {
				ve.add("mcp_servers.%s: %s transport requires url", id, srv.Transport)
			}
		}
	}
}

func validateOutputs(wf *model.Workflow, ve *ValidationError) {
	for key, val := range wf.Outputs {
		checkExprNoStepPiercing("outputs."+key, val, ve)
	}
	for jobID, job := range wf.Jobs {
		for key, val := range job.Outputs {
			ctx := fmt.Sprintf("job %q outputs.%s", jobID, key)
			checkExprNoStepPiercing(ctx, val, ve)
		}
	}
}

func checkExprNoStepPiercing(ctx, expr string, ve *ValidationError) {
	if strings.Contains(expr, ".steps.") || strings.Contains(expr, ".steps}") {
		if strings.Contains(expr, "jobs.") && strings.Contains(expr, ".steps") {
			ve.add("%s: must not pierce into step internals; use jobs.<job_id>.outputs.<key> instead", ctx)
		}
	}
}
