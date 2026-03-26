package agent

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/sjzar/reed/internal/model"
	"github.com/sjzar/reed/internal/skill"
)

// PromptMode controls system prompt verbosity.
// Higher values include more sections.
// Zero value is PromptModeNone (most restrictive mode — only sections with MinMode=0 are included).
// This is intentional: a forgotten MinMode field on a section defaults to 0 (PromptModeNone),
// meaning the section is always visible regardless of mode — the safe default.
type PromptMode uint8

const (
	PromptModeNone    PromptMode = iota // identity only
	PromptModeMinimal                   // identity + tools + sysinfo
	PromptModeFull                      // all sections (default)
)

// ParsePromptMode converts a string to PromptMode. Defaults to PromptModeFull.
func ParsePromptMode(s string) PromptMode {
	switch s {
	case "full", "":
		return PromptModeFull
	case "minimal":
		return PromptModeMinimal
	case "none":
		return PromptModeNone
	default:
		return PromptModeFull
	}
}

// String returns the canonical string representation of the mode.
func (m PromptMode) String() string {
	switch m {
	case PromptModeNone:
		return "none"
	case PromptModeMinimal:
		return "minimal"
	case PromptModeFull:
		return "full"
	default:
		return "full"
	}
}

// DefaultSystemPrompt is used when neither the request nor the profile provides one.
const DefaultSystemPrompt = `You are an autonomous problem-solving agent operating within the Reed workflow orchestration runtime. You execute complex tasks in a fully automated, non-interactive environment. Your absolute priority is to solve the given problem completely and robustly without human intervention.

# Core Principles
- **Total Autonomy:** You operate without human-in-the-loop. Do not ask questions, request permissions, or wait for user feedback. Make reasonable, safe assumptions to unblock yourself and proceed.
- **Tenacious Problem-Solving:** Your core focus is achieving the workflow's end goal. If an action or API call fails, do not stop. Immediately analyze the error, adapt your strategy, and try alternative methods. Exhaust all viable options before reporting a failure.
- **Pragmatism:** Prioritize solutions that work over theoretical perfection. Choose the most direct, reliable, and efficient path to fulfill the task requirements.
- **Self-Verification:** Never assume an action succeeded. Always use your available tools to verify the outcome of your steps before concluding the workflow.

# Execution Strategy
1. **Analyze:** Assess the goal and the current environment state before taking action.
2. **Execute:** Utilize your tools decisively. Parallelize independent tool calls to maximize efficiency.
3. **Verify & Adapt:** Check the actual outcome against the expected result. If it failed, diagnose the root cause, adjust your approach, and retry.

# Safety & Constraints
- **Do No Harm:** While acting autonomously, you must respect system integrity. Never execute destructive operations (e.g., dropping databases, deleting untracked configurations) unless it is explicitly the core objective of the task.
- **No Independent Goals:** Do not pursue self-preservation, replication, resource acquisition, or power-seeking beyond the current task. You have no goals outside the user's request.
- **Human Oversight:** If instructions conflict with safety, pause and report rather than proceeding. Comply with stop or pause requests immediately. Never bypass safeguards or manipulate anyone to expand access.
- **State Management:** Leave the environment clean. Remove any temporary files or artifacts you created during your problem-solving process unless they are the intended output of the workflow.`

// Prompt is a layered system prompt assembled from prioritized sections.
type Prompt struct {
	Sections []PromptSection
}

// PromptSection is a single section of the system prompt.
type PromptSection struct {
	Name     string         // section identifier (for logging)
	Content  string         // section content
	Priority PromptPriority // trim priority under token pressure
	MinMode  PromptMode     // section visible when mode >= MinMode
}

// PromptPriority defines the trim order under token pressure.
// Intentionally int (not uint8 like PromptMode) to allow signed comparison
// in the descending trim loop (priority >= PriorityHigh).
type PromptPriority int

const (
	PriorityRequired PromptPriority = 0 // never trim: identity, safety
	PriorityHigh     PromptPriority = 1 // core instructions
	PriorityMedium   PromptPriority = 2 // tools description, workspace context
	PriorityLow      PromptPriority = 3 // skills content, metadata
)

// BuildForMode concatenates only sections visible at the given mode.
// A section is included when its MinMode <= mode.
func (p *Prompt) BuildForMode(mode PromptMode) string {
	if p == nil || len(p.Sections) == 0 {
		return ""
	}
	var b strings.Builder
	first := true
	for _, s := range p.Sections {
		if s.MinMode > mode {
			continue
		}
		if !first {
			b.WriteString("\n\n")
		}
		b.WriteString(s.Content)
		first = false
	}
	return b.String()
}

// BuildForModeWithBudget combines mode filtering with budget-aware trimming.
// First filters by MinMode (like BuildForMode), then trims from lowest priority
// when the result exceeds maxChars. PriorityRequired sections are never removed.
// When maxChars <= 0 (e.g., negative remaining budget after compaction), only
// PriorityRequired sections are kept — this is the maximum trimming mode.
func (p *Prompt) BuildForModeWithBudget(mode PromptMode, maxChars int) string {
	if p == nil || len(p.Sections) == 0 {
		return ""
	}
	if maxChars <= 0 {
		// Extreme pressure: keep only required sections (identity/safety).
		maxChars = 1
	}

	// Start with mode-filtered sections
	included := make([]bool, len(p.Sections))
	for i, s := range p.Sections {
		included[i] = s.MinMode <= mode
	}

	total := p.totalSize(included)

	// Trim from lowest priority first within mode-visible sections
	for priority := PriorityLow; priority >= PriorityHigh && total > maxChars; priority-- {
		for i := len(p.Sections) - 1; i >= 0 && total > maxChars; i-- {
			if !included[i] || p.Sections[i].Priority != priority {
				continue
			}
			included[i] = false
			total = p.totalSize(included)
		}
	}

	var b strings.Builder
	first := true
	for i, s := range p.Sections {
		if !included[i] {
			continue
		}
		if !first {
			b.WriteString("\n\n")
		}
		b.WriteString(s.Content)
		first = false
	}
	return b.String()
}

// BuildWithBudget builds the prompt within a character budget.
// Trims from lowest priority sections first.
// Note: this method does not filter by MinMode — it operates on all sections.
// Callers needing mode-aware budget trimming should pre-filter or use BuildForMode.
func (p *Prompt) BuildWithBudget(maxChars int) string {
	if p == nil || len(p.Sections) == 0 {
		return ""
	}

	// Start with all sections
	included := make([]bool, len(p.Sections))
	for i := range included {
		included[i] = true
	}

	// Calculate total size
	total := p.totalSize(included)

	// Trim from lowest priority first. The loop stops before PriorityRequired (0),
	// ensuring identity/safety sections are never removed regardless of budget.
	for priority := PriorityLow; priority >= PriorityHigh && total > maxChars; priority-- {
		for i := len(p.Sections) - 1; i >= 0 && total > maxChars; i-- {
			if !included[i] || p.Sections[i].Priority != priority {
				continue
			}
			included[i] = false
			total = p.totalSize(included) // recompute to avoid separator miscounting
		}
	}

	var b strings.Builder
	first := true
	for i, s := range p.Sections {
		if !included[i] {
			continue
		}
		if !first {
			b.WriteString("\n\n")
		}
		b.WriteString(s.Content)
		first = false
	}
	return b.String()
}

func (p *Prompt) totalSize(included []bool) int {
	total := 0
	count := 0
	for i, s := range p.Sections {
		if !included[i] {
			continue
		}
		total += len(s.Content)
		count++
	}
	if count > 1 {
		total += (count - 1) * 2 // separators
	}
	return total
}

// promptOpts holds all resolved inputs for building the system prompt.
type promptOpts struct {
	SystemPrompt  string
	ToolDefs      []model.ToolDef
	SkillInfos    []skill.SkillInfo
	MemoryContent string
	AgentID       string
	Model         string
	Cwd           string
	Timezone      string
	ContextWindow int
}

// buildPrompt assembles the system prompt from resolved inputs (pure function).
func buildPrompt(opts promptOpts) *Prompt {
	p := &Prompt{}

	// Identity (always present)
	if opts.SystemPrompt != "" {
		p.Sections = append(p.Sections, PromptSection{
			Name: "identity", Content: opts.SystemPrompt, Priority: PriorityRequired, MinMode: PromptModeNone,
		})
	}

	// Tool summary
	if summary := buildToolSummary(opts.ToolDefs); summary != "" {
		p.Sections = append(p.Sections, PromptSection{
			Name: "tools", Content: summary, Priority: PriorityMedium, MinMode: PromptModeMinimal,
		})
	}

	// System info
	if sysInfo := buildSystemInfo(opts.AgentID, opts.Model, opts.Cwd, runtime.GOOS, runtime.GOARCH, opts.Timezone, opts.ContextWindow); sysInfo != "" {
		p.Sections = append(p.Sections, PromptSection{
			Name: "sysinfo", Content: sysInfo, Priority: PriorityMedium, MinMode: PromptModeMinimal,
		})
	}

	// Memory
	if opts.MemoryContent != "" {
		p.Sections = append(p.Sections, PromptSection{
			Name: "memory", Content: opts.MemoryContent, Priority: PriorityMedium, MinMode: PromptModeFull,
		})
	}

	// Skills
	if len(opts.SkillInfos) > 0 {
		p.Sections = append(p.Sections, PromptSection{
			Name: "skills", Content: buildSkillSummary(opts.SkillInfos), Priority: PriorityLow, MinMode: PromptModeFull,
		})
	} else {
		p.Sections = append(p.Sections, PromptSection{
			Name:     "skills",
			Content:  "No skills are loaded for this run.",
			Priority: PriorityLow,
			MinMode:  PromptModeFull,
		})
	}

	return p
}

// buildSkillSummary formats mounted skill info into a prompt section.
func buildSkillSummary(infos []skill.SkillInfo) string {
	var b strings.Builder
	b.WriteString("## Available Skills\nWhen a skill matches the task, read its SKILL.md for complete instructions.\n\n")
	for _, info := range infos {
		fmt.Fprintf(&b, "- %s: %s (read: %s/SKILL.md)\n", info.ID, info.Description, info.MountDir)
	}
	b.WriteString("\nEach skill's entry point is its SKILL.md. Other files in the same directory also belong to the skill.\n")
	return b.String()
}

// buildProgressHint returns a short progress line for ephemeral injection.
// iteration is 0-based internally but displayed as 1-based.
// Returns "" when iteration is 0 (first call — no useful progress info).
func buildProgressHint(iteration, maxIter, estimatedTokens, contextWindow int) string {
	if iteration <= 0 {
		return ""
	}
	display := iteration + 1 // 1-based for the model
	if contextWindow > 0 {
		return fmt.Sprintf("[Progress: iteration %d/%d | context ~%d/%d tokens]", display, maxIter, estimatedTokens, contextWindow)
	}
	return fmt.Sprintf("[Progress: iteration %d/%d]", display, maxIter)
}
