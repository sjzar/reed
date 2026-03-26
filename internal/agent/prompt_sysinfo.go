package agent

import (
	"fmt"
	"strings"
)

// buildSystemInfo formats system information into a prompt section.
// Returns "" when all fields are empty, avoiding a header-only waste of tokens.
// timezone is a stable string (e.g., "Asia/Shanghai") for prompt cache stability —
// a dynamic timestamp would break cache prefix matching on every run.
func buildSystemInfo(agentID, modelName, cwd, osName, arch, timezone string, contextWindow int) string {
	var b strings.Builder
	if osName != "" {
		if arch != "" {
			fmt.Fprintf(&b, "- OS: %s (%s)\n", osName, arch)
		} else {
			fmt.Fprintf(&b, "- OS: %s\n", osName)
		}
	}
	if cwd != "" {
		fmt.Fprintf(&b, "- Working Directory: %s\n", cwd)
	}
	if agentID != "" {
		fmt.Fprintf(&b, "- Agent: %s\n", agentID)
	}
	if modelName != "" {
		fmt.Fprintf(&b, "- Model: %s\n", modelName)
	}
	if timezone != "" {
		fmt.Fprintf(&b, "- Timezone: %s\n", timezone)
	}
	if contextWindow > 0 {
		fmt.Fprintf(&b, "- Context Window: %d tokens\n", contextWindow)
	}
	if b.Len() == 0 {
		return ""
	}
	return "## System Information\n" + b.String()
}
