package reed

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/sjzar/reed/internal/bus"
	"github.com/sjzar/reed/internal/db"
	"github.com/sjzar/reed/internal/model"
	reedmgr "github.com/sjzar/reed/internal/reed"
)

var doCmd = &cobra.Command{
	Use:   `do "<prompt>"`,
	Short: "Run a standalone agent loop without a workflow.",
	Long: `Run a standalone agent loop for quick tasks and skill testing.
Bypasses the workflow/engine layer and directly invokes the agent engine.

Examples:
  reed do "hello"
  reed do "list files" --tool-access full
  reed do "test" --skill my-skill -s another
  reed do "test" --session-key foo`,
	Args:          requireArgs(1),
	RunE:          runDo,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	doCmd.Flags().StringP("model", "m", "", "LLM model override")
	doCmd.Flags().StringSliceP("skill", "s", nil, "skill IDs or remote URLs to mount (repeatable)")
	doCmd.Flags().StringSliceP("tool", "t", nil, "tool IDs to enable (repeatable)")
	doCmd.Flags().String("system-prompt", "", "custom system prompt")
	doCmd.Flags().Int("max-iterations", 0, "max agent loop iterations")
	doCmd.Flags().String("session-key", "", "reuse session across invocations")
	doCmd.Flags().String("namespace", "", "session namespace (default: sanitized cwd hash)")
	doCmd.Flags().String("tool-access", "workdir", `tool access profile: "workdir" or "full"`)
	doCmd.Flags().String("thinking-level", "", "thinking level")
	rootCmd.AddCommand(doCmd)
}

func runDo(cmd *cobra.Command, args []string) error {
	cfg := loadConfig(cmd)

	cleanupLogger := initLogger(cfg, LogModeShared, "")
	defer func() {
		if cleanupLogger != nil {
			cleanupLogger()
		}
	}()

	prompt := args[0]

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	// Validate --tool-access
	toolAccess, _ := cmd.Flags().GetString("tool-access")
	if toolAccess != "workdir" && toolAccess != "full" {
		return fmt.Errorf("invalid --tool-access %q: must be \"workdir\" or \"full\"", toolAccess)
	}

	// Build sanitized namespace from CWD (or --namespace flag)
	namespace, _ := cmd.Flags().GetString("namespace")
	if namespace == "" {
		namespace = sanitizeNamespace(cwd)
	}

	// Open DB for session routes only
	d, err := db.Open(cfg.DBDir())
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer d.Close()

	// Build agent
	buildResult, err := reedmgr.BuildAgent(cmd.Context(), reedmgr.BuildAgentConfig{
		WorkDir:     cwd,
		Models:      cfg.Models,
		RouteStore:  db.NewSessionRouteRepo(d),
		SessionDir:  cfg.SessionDir(),
		HomeDir:     cfg.Home,
		SkillModDir: cfg.SkillModDir(),
		MemoryDir:   cfg.MemoryDir(),
	})
	if err != nil {
		return fmt.Errorf("build agent: %w", err)
	}
	defer buildResult.Close()

	// Build AgentRunRequest from CLI flags
	modelRef, _ := cmd.Flags().GetString("model")
	systemPrompt, _ := cmd.Flags().GetString("system-prompt")
	sessionKey, _ := cmd.Flags().GetString("session-key")
	thinkingLevel, _ := cmd.Flags().GetString("thinking-level")
	maxIterations, _ := cmd.Flags().GetInt("max-iterations")
	skills, _ := cmd.Flags().GetStringSlice("skill")

	// Resolve remote skill URLs (e.g. GitHub URLs) before building the request
	if len(skills) > 0 {
		skills, err = buildResult.Skills.ResolveCLISkills(cmd.Context(), skills)
		if err != nil {
			return fmt.Errorf("resolve skills: %w", err)
		}
	}

	// nil vs empty tool semantics: nil = core tools, empty = no tools
	var tools []string
	if cmd.Flags().Changed("tool") {
		tools, _ = cmd.Flags().GetStringSlice("tool")
	}

	// Create temp run root
	runRoot, err := os.MkdirTemp("", "reed-do-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(runRoot)

	stepRunID := "do_" + uuid.New().String()[:8]
	eventBus := bus.New()

	// Start streaming goroutine
	done := reedmgr.WriteTextDeltas(eventBus, stepRunID, os.Stdout)

	// Set up signal handling (derive from cmd.Context() to respect parent cancellation)
	ctx, cancel := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	req := &model.AgentRunRequest{
		Namespace:         namespace,
		AgentID:           "do",
		SessionKey:        sessionKey,
		Model:             modelRef,
		SystemPrompt:      systemPrompt,
		Tools:             tools,
		Skills:            skills,
		Prompt:            prompt,
		MaxIterations:     maxIterations,
		ThinkingLevel:     thinkingLevel,
		WaitForAsyncTasks: true,
		Bus:               eventBus,
		StepRunID:         stepRunID,
		RunRoot:           runRoot,
		Cwd:               cwd,
		ToolAccessProfile: toolAccess,
	}

	resp, err := buildResult.Runner.Run(ctx, req)

	// Close bus to signal streamer, then wait for drain
	eventBus.Close()
	<-done

	if err != nil {
		return err
	}

	// Print summary
	fmt.Fprintf(os.Stderr, "\n\n[do] %s  iterations=%d  tokens=%d\n",
		resp.StopReason, resp.Iterations, resp.TotalUsage.Total)
	return nil
}

// sanitizeNamespace derives a filesystem-safe namespace from a directory path.
// Uses the base directory name with a short hash suffix to avoid collisions.
func sanitizeNamespace(dir string) string {
	base := filepath.Base(dir)
	base = strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(base)
	if base == "" || base == "." || base == ".." {
		base = "default"
	}
	h := sha256.Sum256([]byte(dir))
	return base + "_" + hex.EncodeToString(h[:4])
}
