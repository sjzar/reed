package reed

import (
	"fmt"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/sjzar/reed/internal/skill"
)

var skillCmd = &cobra.Command{
	Use:   "skill",
	Short: "Manage installed skills.",
	Long: `Manage installed skills.

Skills can be installed from remote sources (GitHub repositories, HTTPS URLs)
and registered in a local or global manifest. Project-local skills in ./skills/
have the highest priority and are scanned automatically.

Subcommands:
  install     Install a skill from a remote source
  uninstall   Remove a skill from the manifest
  list        List all installed skills
  tidy        Re-download missing skills`,
}

var skillInstallCmd = &cobra.Command{
	Use:   "install <source>",
	Short: "Install a skill from a remote source.",
	Long: `Install a skill from a remote source.

Downloads the skill to the mod cache (~/.reed/mod/skills/) and registers
it in the local manifest (.reed/skills.json) or global manifest with -g.

Arguments:
  source    GitHub repository or HTTPS URL
            github.com/user/repo           all skills in repo
            github.com/user/repo@v1.0      pinned version
            github.com/user/repo/path      specific skill
            https://example.com/.../SKILL.md  single file

Examples:
  reed skill install github.com/user/my-skills
  reed skill install github.com/user/my-skills@v1.0 -g`,
	Args:          requireArgs(1),
	RunE:          runSkillInstall,
	SilenceUsage:  true,
	SilenceErrors: true,
}

var skillUninstallCmd = &cobra.Command{
	Use:   "uninstall <name>",
	Short: "Remove a skill from the manifest.",
	Long: `Remove a skill from the manifest.

Removes the skill entry from the local (.reed/skills.json) or global
manifest (-g). Does not delete the mod cache.

Examples:
  reed skill uninstall code-review
  reed skill uninstall code-review -g`,
	Args:          requireArgs(1),
	RunE:          runSkillUninstall,
	SilenceUsage:  true,
	SilenceErrors: true,
}

var skillListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all installed skills.",
	Long: `List all installed skills across all scopes.

Shows skills from project (./skills/), local (.reed/skills.json),
and global (~/.reed/skills.json) manifests. Shadowed and missing
skills are annotated.`,
	RunE:          runSkillList,
	SilenceUsage:  true,
	SilenceErrors: true,
}

var skillTidyCmd = &cobra.Command{
	Use:   "tidy",
	Short: "Re-download missing skills from their original sources.",
	Long: `Re-download missing skills from their original sources.

Checks all installed skills in the manifest and re-downloads any whose
mod cache directory is missing.

Without -g, tidies the local manifest (.reed/skills.json).`,
	RunE:          runSkillTidy,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	skillInstallCmd.Flags().BoolP("global", "g", false, "install to global scope (~/.reed/skills.json)")
	skillUninstallCmd.Flags().BoolP("global", "g", false, "uninstall from global scope (~/.reed/skills.json)")
	skillTidyCmd.Flags().BoolP("global", "g", false, "tidy global manifest only (~/.reed/skills.json)")

	skillCmd.AddCommand(skillInstallCmd)
	skillCmd.AddCommand(skillUninstallCmd)
	skillCmd.AddCommand(skillListCmd)
	skillCmd.AddCommand(skillTidyCmd)
	rootCmd.AddCommand(skillCmd)
}

func runSkillInstall(cmd *cobra.Command, args []string) error {
	cfg := loadConfig(cmd)
	source := args[0]
	global, _ := cmd.Flags().GetBool("global")

	scope := skill.ScopeLocal
	if global {
		scope = skill.ScopeGlobal
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	result, err := skill.Install(cmd.Context(), source, scope, cwd, cfg.Home, cfg.SkillModDir())
	if err != nil {
		return err
	}

	for _, name := range result.Installed {
		fmt.Fprintf(os.Stdout, "installed %s (%s)\n", name, scope)
	}
	return nil
}

func runSkillUninstall(cmd *cobra.Command, args []string) error {
	cfg := loadConfig(cmd)
	name := args[0]
	global, _ := cmd.Flags().GetBool("global")

	scope := skill.ScopeLocal
	if global {
		scope = skill.ScopeGlobal
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	if err := skill.Uninstall(name, scope, cwd, cfg.Home); err != nil {
		return err
	}

	fmt.Fprintf(os.Stdout, "uninstalled %s (%s)\n", name, scope)
	return nil
}

func runSkillList(cmd *cobra.Command, _ []string) error {
	cfg := loadConfig(cmd)

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	svc := skill.New(cfg.Home, cfg.SkillModDir())
	result, err := svc.ScanInstalledDiag(cmd.Context(), cwd)
	if err != nil {
		return err
	}

	if len(result.Entries) == 0 {
		fmt.Fprintln(os.Stdout, "No skills installed.")
		return nil
	}

	// Sort entries: effective first, then shadowed, then missing
	entries := result.Entries
	sort.Slice(entries, func(i, j int) bool {
		si, sj := sortKey(entries[i]), sortKey(entries[j])
		if si != sj {
			return si < sj
		}
		return entries[i].Name < entries[j].Name
	})

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tREF\tSCOPE\tSOURCE")
	var missingCount int
	for _, e := range entries {
		name := e.Name
		if e.IsShadowed {
			name += " (SHADOWED)"
		}
		if e.IsMissing {
			name += " (MISSING)"
			missingCount++
		}
		ref := formatRef(e.Ref, e.Commit)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", name, ref, e.Scope, e.Source)
	}
	tw.Flush()

	if missingCount > 0 {
		fmt.Fprintf(os.Stderr, "\nFound %d missing skill(s). Run 'reed skill tidy' to re-download.\n", missingCount)
	}
	return nil
}

func runSkillTidy(cmd *cobra.Command, _ []string) error {
	cfg := loadConfig(cmd)
	global, _ := cmd.Flags().GetBool("global")

	scope := skill.ScopeLocal
	if global {
		scope = skill.ScopeGlobal
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	result, err := skill.Tidy(cmd.Context(), scope, cwd, cfg.Home, cfg.SkillModDir())
	if err != nil {
		return err
	}

	if len(result.Fixed) == 0 && len(result.Failed) == 0 {
		fmt.Fprintln(os.Stdout, "All skills are present.")
		return nil
	}

	for _, name := range result.Fixed {
		fmt.Fprintf(os.Stdout, "restored %s\n", name)
	}
	for _, name := range result.Failed {
		fmt.Fprintf(os.Stderr, "failed to restore %s — run 'reed skill uninstall %s' to remove\n", name, name)
	}
	return nil
}

// sortKey returns an integer for ordering: 0=effective, 1=shadowed, 2=missing.
func sortKey(e skill.SkillEntry) int {
	if e.IsMissing {
		return 2
	}
	if e.IsShadowed {
		return 1
	}
	return 0
}

// formatRef formats the ref+commit for display.
func formatRef(ref, commit string) string {
	if ref == "" {
		return ""
	}
	if commit == "" {
		return ref
	}
	short := commit
	if len(short) > 7 {
		short = short[:7]
	}
	return ref + "@" + short
}
