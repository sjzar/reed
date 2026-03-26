package reed

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sjzar/reed/internal/builtin"
	"github.com/sjzar/reed/internal/model"
	reedmgr "github.com/sjzar/reed/internal/reed"
)

var planCmd = &cobra.Command{
	Use:   `plan "<prompt>"`,
	Short: "Generate a workflow YAML file from a natural language description.",
	Long: `Generate a Reed workflow YAML file by running an embedded agent workflow.
The agent understands the Reed DSL and creates a well-structured workflow file
based on your description.

Examples:
  reed plan "a workflow that runs tests and lints code"
  reed plan "an agent that reviews pull requests"
  reed plan "a service workflow that listens on port 8080 for webhooks"`,
	Args:          requireArgs(1),
	RunE:          runPlan,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.AddCommand(planCmd)
}

func runPlan(cmd *cobra.Command, args []string) error {
	cfg := loadConfig(cmd)

	cleanupLogger := initLogger(cfg, LogModeShared, "")
	defer func() {
		if cleanupLogger != nil {
			cleanupLogger()
		}
	}()

	wf, err := builtin.LoadWorkflow("create-workflow")
	if err != nil {
		return fmt.Errorf("load builtin workflow: %w", err)
	}

	workDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	m, err := reedmgr.New(cfg)
	if err != nil {
		return err
	}

	result, execErr := m.Execute(cmd.Context(), reedmgr.ExecuteOpts{
		Workflow:       wf,
		WorkDir:        workDir,
		WorkflowSource: wf.Source,
		Mode:           model.ProcessModeCLI,
		Inputs:         []string{"prompt=" + args[0]},
		SecretSources:  cfg.Secrets,
		Stdout:         os.Stdout,
		SwitchLogger: func(processID string) func() {
			return initLogger(cfg, LogModeCLI, processID)
		},
	})

	if result != nil && result.LoggerCleanup != nil {
		cleanupLogger()
		cleanupLogger = result.LoggerCleanup
	}

	if result != nil {
		for _, rv := range result.TerminalRuns {
			printRunSummary(os.Stdout, rv)
		}
	}

	return execErr
}
