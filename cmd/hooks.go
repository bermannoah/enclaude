package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/coredipper/enclaude/internal/config"
	"github.com/coredipper/enclaude/internal/gitops"
	"github.com/spf13/cobra"
)

var hooksCmd = &cobra.Command{
	Use:   "hooks",
	Short: "Manage agent hook integration",
}

var hooksInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install auto-sync hooks into the configured agent",
	Long: `Adds session lifecycle hooks that unseal on start and seal on end.

Claude Code hooks are written to <source-dir>/settings.json. pi.dev hooks are
installed as a managed extension at <source-dir>/extensions/enclaude.ts.
Existing settings and extensions are preserved.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		source, dir, err := resolveHookTarget()
		if err != nil {
			return err
		}

		if source == config.SourcePi {
			if gitops.PiHooksInstalled(dir) {
				fmt.Println("Seal hooks are already installed.")
				return nil
			}
			fmt.Println("Installing seal extension for pi.dev...")
			if err := gitops.InstallPiHooks(dir, getSealDir()); err != nil {
				return fmt.Errorf("installing pi hooks: %w", err)
			}
			fmt.Printf("  Wrote %s\n", gitops.PiExtensionPath(dir))
			fmt.Println("  session_start:    pull + unseal")
			fmt.Println("  session_shutdown: seal + push")
			fmt.Println("\nHooks installed. Existing extensions were preserved.")
			return nil
		}

		if gitops.HooksInstalled(dir) {
			fmt.Println("Seal hooks are already installed.")
			return nil
		}

		fmt.Println("Installing seal hooks into settings.json...")
		if err := gitops.InstallHooks(dir, getSealDir()); err != nil {
			return fmt.Errorf("installing hooks: %w", err)
		}

		fmt.Println("  SessionStart hook: pull + unseal on session start")
		fmt.Println("  SessionEnd hook:   seal + push on session end (async)")
		fmt.Println("\nHooks installed. Existing hooks were preserved.")
		return nil
	},
}

var hooksRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove seal hooks from the configured agent",
	RunE: func(cmd *cobra.Command, args []string) error {
		source, dir, err := resolveHookTarget()
		if err != nil {
			return err
		}

		if source == config.SourcePi {
			if !gitops.PiHooksInstalled(dir) {
				fmt.Println("No seal hooks found.")
				return nil
			}
			fmt.Println("Removing seal extension...")
			if err := gitops.RemovePiHooks(dir); err != nil {
				return fmt.Errorf("removing pi hooks: %w", err)
			}
			fmt.Println("Seal hooks removed. Other extensions were preserved.")
			return nil
		}

		if !gitops.HooksInstalled(dir) {
			fmt.Println("No seal hooks found.")
			return nil
		}

		fmt.Println("Removing seal hooks...")
		if err := gitops.RemoveHooks(dir); err != nil {
			return fmt.Errorf("removing hooks: %w", err)
		}

		fmt.Println("Seal hooks removed. Other hooks were preserved.")
		return nil
	},
}

var hooksStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check if seal hooks are installed",
	RunE: func(cmd *cobra.Command, args []string) error {
		source, dir, err := resolveHookTarget()
		if err != nil {
			return err
		}

		installed := false
		if source == config.SourcePi {
			installed = gitops.PiHooksInstalled(dir)
		} else {
			installed = gitops.HooksInstalled(dir)
		}

		if installed {
			fmt.Printf("Seal hooks (%s): installed\n", source)
		} else {
			fmt.Printf("Seal hooks (%s): not installed\n", source)
			fmt.Println("Run 'enclaude hooks install' to enable auto-sync.")
		}
		return nil
	},
}

// resolveHookTarget prefers the source recorded in the seal store so hooks are
// installed into the right agent even when the source flag is omitted. It fails
// closed on a non-default source_dir that the user has not confirmed locally.
func resolveHookTarget() (source, dir string, err error) {
	sealDir := getSealDir()
	if _, statErr := os.Stat(filepath.Join(sealDir, "seal.toml")); statErr == nil {
		cfg, loadErr := config.Load(sealDir)
		if loadErr == nil {
			dir, err := resolveSourceDir(cfg)
			if err != nil {
				return "", "", err
			}
			return cfg.Source(), dir, nil
		}
	}
	return getSource(), getSourceDir(), nil
}

func init() {
	hooksCmd.AddCommand(hooksInstallCmd)
	hooksCmd.AddCommand(hooksRemoveCmd)
	hooksCmd.AddCommand(hooksStatusCmd)
	rootCmd.AddCommand(hooksCmd)
}
