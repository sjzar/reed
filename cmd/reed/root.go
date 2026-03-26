package reed

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sjzar/reed/internal/conf"

	// Register AI handler implementations via init().
	_ "github.com/sjzar/reed/internal/ai/handler/anthropic"
	_ "github.com/sjzar/reed/internal/ai/handler/openai"
	_ "github.com/sjzar/reed/internal/ai/handler/openai_responses"
)

var rootCmd = &cobra.Command{
	Use:   conf.AppName,
	Short: "Headless agent runtime — execute DAG workflows that orchestrate LLM reasoning, shell commands, and HTTP requests.",
	Long: `Reed is a headless agent runtime that anchors dynamic LLM reasoning within deterministic
DAG workflows. Exposed as a CLI for seamless LLM invocation, it provides a structured
execution environment for complex, automated tasks.

A successful "reed run -d" returns a process ID such as "proc_ab12cd34". Process-targeted
commands (status, logs, stop) accept either that process ID or the numeric OS PID.`,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().Bool("debug", false, "enable debug-level logging for this command")
	rootCmd.PersistentFlags().StringP("home", "", "", "override the Reed state directory path for this command")

	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if home, _ := cmd.Flags().GetString("home"); home != "" {
			os.Setenv("REED_HOME", home)
		}
		return nil
	}

	rootCmd.AddCommand(versionCmd)
}

// requireArgs returns a cobra.PositionalArgs that validates the arg count.
// On failure it prints the full help text (same as --help) so that callers
// — whether human or LLM agent — get all the information needed to
// self-correct in a single round-trip, following ACI design principles.
func requireArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) >= n {
			return nil
		}
		// Print the full help (identical to --help) before the error.
		cmd.Help()
		fmt.Fprintln(cmd.ErrOrStderr())
		return fmt.Errorf("missing required argument")
	}
}

// loadConfig reads conf.Load() and applies CLI flag overrides.
// cmd may be nil (e.g. resolveDetachedProcessID), in which case no overrides are applied.
func loadConfig(cmd *cobra.Command) *conf.Config {
	cfg, err := conf.Load()
	if err != nil {
		cfg = &conf.Config{}
	}

	if cmd == nil {
		return cfg
	}

	if debug, _ := cmd.Flags().GetBool("debug"); debug {
		cfg.Debug = true
	}
	return cfg
}
