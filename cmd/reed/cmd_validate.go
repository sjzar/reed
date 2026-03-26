package reed

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sjzar/reed/internal/workflow"
)

var validateCmd = &cobra.Command{
	Use:   "validate <workflow-source>",
	Short: "Check if a workflow file or URL is valid without executing it.",
	Long: `Parse and validate a workflow from a local file or URL without executing it.

Use when: you want to check workflow syntax and structure before running it.
Do not use when: you want to execute the workflow; use "reed run" instead.

Checks: YAML syntax, required fields, job and step structure, DAG validity, and expression syntax.
Prints "workflow is valid" on success, or a descriptive error on failure.

Examples:
  reed validate build.yml
  reed validate https://example.com/workflow.yml`,
	Args:          requireArgs(1),
	RunE:          runValidate,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.AddCommand(validateCmd)
}

func runValidate(cmd *cobra.Command, args []string) error {
	cfg := loadConfig(cmd)
	cleanup := initLogger(cfg, LogModeShared, "")
	defer cleanup()

	if err := workflow.ValidateFile(args[0]); err != nil {
		return err
	}
	fmt.Println("workflow is valid")
	return nil
}
