package reed

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sjzar/reed/internal/model"
	reedmgr "github.com/sjzar/reed/internal/reed"
	"github.com/sjzar/reed/internal/workflow"
	"github.com/sjzar/reed/pkg/reexec"
)

var runCmd = &cobra.Command{
	Use:   "run <workflow-source> [workflow-command]",
	Short: "Start a workflow from a local file or URL. Waits for completion by default.",
	Long: `Start a workflow and execute its jobs.

Use when: you want to execute a workflow now.
Do not use when: you only want to check workflow syntax; use "reed validate" instead.

By default, "reed run" blocks until the workflow finishes and prints step events followed
by a final summary (status, failed steps, outputs). Exit code 0 on success, non-zero on failure.
Use "-d" when you want background execution and only need the ProcessID returned immediately.
Use "[workflow-command]" only when the workflow defines named CLI commands; otherwise omit it.

Flags:
  --set       Override workflow fields before execution. Format: key=value, repeatable.
              Values are type-inferred (numbers become numbers, "true"/"false" become bools).
              Use --set-string when the value must stay a string. Example: --set model=gpt-4
  --set-string Force the value to remain a string. Format: key=value, repeatable.
              Use instead of --set when the value looks like a number or bool but should be
              treated as a string. Example: --set-string version=3.0
  --set-file  Merge a local YAML/JSON patch file into the workflow before execution (RFC 7386).
              Repeatable; multiple patches are applied in order.
  --env       Set or override a workflow environment variable. Format: NAME=value, repeatable.
  --input     Provide a workflow input value. Format: inputName=value, repeatable.
              For media-type inputs, pass a local file path and it will be uploaded automatically.

Examples:
  reed run build.yml                    Run a workflow and wait for completion
  reed run https://example.com/wf.yml   Run a workflow from a URL
  reed run deploy.yml --env ENV=prod    Run with environment variable override
  reed run app.yml -d                   Run in background, returns ProcessID immediately
  reed run api.yml serve                Run a named workflow command
  reed run pipeline.yml -i image=a.png  Run with input value`,
	Args:          requireArgs(1),
	RunE:          runRun,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	runCmd.Flags().BoolP("detach", "d", false, "run in background and return ProcessID immediately instead of waiting")
	runCmd.Flags().StringArrayP("set", "", nil, "override workflow field (key=value); values are type-inferred. Repeatable")
	runCmd.Flags().StringArrayP("set-file", "", nil, "merge a local YAML/JSON patch file into the workflow. Repeatable, applied in order")
	runCmd.Flags().StringArray("set-string", nil, "override workflow field as string (key=value); prevents type inference. Repeatable")
	runCmd.Flags().StringArray("env", nil, "set workflow environment variable (NAME=value). Repeatable")
	runCmd.Flags().StringArrayP("input", "i", nil, "provide workflow input value (inputName=value). Repeatable; use a local file path for media inputs")
	rootCmd.AddCommand(runCmd)
}

func runRun(cmd *cobra.Command, args []string) error {
	cfg := loadConfig(cmd)

	// Phase 1: shared logger before process registration
	cleanupLogger := initLogger(cfg, LogModeShared, "")
	defer func() {
		if cleanupLogger != nil {
			cleanupLogger()
		}
	}()

	// Detach mode: re-exec self in background
	if detach, _ := cmd.Flags().GetBool("detach"); detach {
		cleanupLogger()
		cleanupLogger = nil
		return detachProcess(os.Args)
	}

	// Detect detach pipe: child process inherits a write fd via this env var.
	pipe := reexec.PipeFromEnv()
	if pipe != nil {
		_ = cmd.Flags().Set("detach", "false")
	}

	// Prepare workflow
	sets, _ := cmd.Flags().GetStringArray("set")
	setFiles, _ := cmd.Flags().GetStringArray("set-file")
	setStrings, _ := cmd.Flags().GetStringArray("set-string")
	envs, _ := cmd.Flags().GetStringArray("env")
	inputs, _ := cmd.Flags().GetStringArray("input")

	wf, err := workflow.PrepareWorkflow(args[0], setFiles, sets, setStrings, envs)
	if err != nil {
		pipe.SignalError(err)
		return err
	}

	workDir, err := os.Getwd()
	if err != nil {
		pipe.SignalError(err)
		return fmt.Errorf("get working directory: %w", err)
	}

	mode := reedmgr.DeriveProcessMode(wf.On)

	m, err := reedmgr.New(cfg)
	if err != nil {
		pipe.SignalError(err)
		return err
	}

	// Build execute options
	execOpts := reedmgr.ExecuteOpts{
		Workflow:       wf,
		WorkDir:        workDir,
		WorkflowSource: args[0],
		Mode:           mode,
		Args:           args,
		Envs:           envs,
		Inputs:         inputs,
		SecretSources:  cfg.Secrets,
		Stdout:         os.Stdout,
		SwitchLogger: func(processID string) func() {
			logMode := LogModeCLI
			if mode == model.ProcessModeService || mode == model.ProcessModeSchedule {
				logMode = LogModeService
			}
			return initLogger(cfg, logMode, processID)
		},
	}

	// Wire up detach readiness callback
	if pipe != nil {
		execOpts.OnReady = func(processID string) {
			pipe.SignalOK(processID)
		}
	}

	result, execErr := m.Execute(cmd.Context(), execOpts)
	if execErr != nil {
		pipe.SignalError(execErr)
	}

	// Switch logger cleanup to per-process logger if available
	if result != nil && result.LoggerCleanup != nil {
		cleanupLogger()
		cleanupLogger = result.LoggerCleanup
	}

	// Print run summaries
	if result != nil {
		for _, rv := range result.TerminalRuns {
			printRunSummary(os.Stdout, rv)
		}
	}

	return execErr
}
