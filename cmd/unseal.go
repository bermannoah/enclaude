package cmd

import (
	"fmt"

	"github.com/coredipper/enclaude/internal/config"
	"github.com/coredipper/enclaude/internal/crypto"
	"github.com/coredipper/enclaude/internal/store"
	"github.com/coredipper/enclaude/internal/ui"
	"github.com/spf13/cobra"
)

var unsealCmd = &cobra.Command{
	Use:   "unseal",
	Short: "Decrypt seal contents to the source directory",
	Long:  "Decrypt all sealed objects and restore them to the configured source directory.",
	RunE:  runUnseal,
}

func init() {
	rootCmd.AddCommand(unsealCmd)
}

func runUnseal(cmd *cobra.Command, args []string) error {
	sealDir := getSealDir()

	cfg, err := config.Load(sealDir)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	sourceDir, err := resolveSourceDir(cfg)
	if err != nil {
		return err
	}
	cfg.Seal.SourceDir = sourceDir

	if flagDryRun {
		fmt.Println("(dry run — showing what would change)")
		diff, err := store.UnsealStatus(cfg)
		if err != nil {
			return fmt.Errorf("unseal status: %w", err)
		}
		if len(diff.Added) == 0 && len(diff.Modified) == 0 && len(diff.Deleted) == 0 {
			fmt.Println("  No changes needed.")
		} else {
			for _, p := range diff.Added {
				fmt.Printf("  [restore] %s\n", p)
			}
			for _, p := range diff.Modified {
				fmt.Printf("  [update] %s\n", p)
			}
			for _, p := range diff.Deleted {
				fmt.Printf("  [delete] %s\n", p)
			}
		}
		return nil
	}

	release, err := acquireSealLock(sealDir)
	if err != nil {
		return err
	}
	defer release()

	identity, source, err := crypto.LoadKey(sealDir)
	if err != nil {
		return fmt.Errorf("loading key: %w", err)
	}
	if flagVerbose {
		fmt.Printf("Using key from %s\n", source)
	}

	fmt.Println("Unsealing...")
	stats, err := store.Unseal(cfg, identity, store.UnsealOptions{Verbose: flagVerbose, AllowSensitiveResources: flagYes})
	if err != nil {
		return fmt.Errorf("unseal: %w", err)
	}
	fmt.Printf("  %s\n", stats)

	warnSkippedSensitive(stats)

	if stats.Errors > 0 {
		fmt.Printf("  Warning: %d errors encountered.\n", stats.Errors)
	}

	return nil
}

// warnSkippedSensitive prints the sensitive resources that were not written
// because the user did not pass --yes, with a short reason for each.
func warnSkippedSensitive(stats store.UnsealStats) {
	if len(stats.SkippedSensitive) == 0 {
		return
	}
	fmt.Printf("\n%s %d sensitive resource(s) were not written:\n",
		ui.Yellow("Skipped:"), len(stats.SkippedSensitive))
	for _, p := range stats.SkippedSensitive {
		fmt.Printf("  - %s (%s)\n", p, store.SensitiveReason(p))
	}
	fmt.Println("Review with 'enclaude diff', then re-run with 'enclaude unseal --yes' to apply them.")
}
