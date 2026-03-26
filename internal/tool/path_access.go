package tool

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/sjzar/reed/internal/security"
)

// resolveFSPath resolves a path using RuntimeContext if available,
// otherwise falls back to filepath.Abs + security.Canonicalize.
// Always canonicalizes to prevent symlink escapes.
func resolveFSPath(ctx RuntimeContext, rawPath string) (string, error) {
	if ctx.Set && ctx.Cwd != "" {
		return ResolvePath(ctx.Cwd, rawPath)
	}
	// Fallback for non-workflow context — still canonicalize to prevent symlink escapes.
	abs, err := filepath.Abs(rawPath)
	if err != nil {
		return "", err
	}
	return security.Canonicalize(abs)
}

// checkReadAccess checks read permission via the security Guard in context.
// Fail-closed: returns error if no Guard is present.
func checkReadAccess(ctx context.Context, resolved string) error {
	checker := security.FromContext(ctx)
	if checker == nil {
		return fmt.Errorf("access denied: no security context")
	}
	return checker.CheckRead(resolved)
}

// checkWriteAccess checks write permission via the security Guard in context.
// Fail-closed: returns error if no Guard is present.
func checkWriteAccess(ctx context.Context, resolved string) error {
	checker := security.FromContext(ctx)
	if checker == nil {
		return fmt.Errorf("access denied: no security context")
	}
	return checker.CheckWrite(resolved)
}

// formatLinesWithNumbers prefixes each line with a right-aligned line number.
// Format: "  N| content" where N width adapts to the maximum line number.
func formatLinesWithNumbers(lines []string, startLine int) string {
	if len(lines) == 0 {
		return ""
	}
	maxLineNum := startLine + len(lines) - 1
	width := len(fmt.Sprintf("%d", maxLineNum))
	if width < 4 {
		width = 4
	}

	var b strings.Builder
	fmtStr := fmt.Sprintf("%%%dd| ", width)
	for i, line := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, fmtStr, startLine+i)
		b.WriteString(line)
	}
	return b.String()
}
