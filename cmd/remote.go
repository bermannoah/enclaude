package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/coredipper/enclaude/internal/config"
	"github.com/coredipper/enclaude/internal/gitops"
	"github.com/spf13/cobra"
)

var remoteCmd = &cobra.Command{
	Use:   "remote",
	Short: "Manage git remotes for sync",
}

// mergeDriverCommand builds the git merge-driver command line. It pins the
// executable, source profile, and seal dir so the driver works from git's
// environment, where no --source flag is present.
func mergeDriverCommand(sealDir string) string {
	exe, err := os.Executable()
	if err != nil || exe == "" {
		exe = "enclaude"
	}
	source := getSource()
	if cfg, err := config.Load(sealDir); err == nil {
		source = cfg.Source()
	}
	return fmt.Sprintf("%s --source %s --seal-dir %s merge-driver manifest %%O %%A %%B",
		shellQuote(exe), shellQuote(source), shellQuote(sealDir))
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

var remoteAddCmd = &cobra.Command{
	Use:   "add <name> <url>",
	Short: "Add a git remote",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		sealDir := getSealDir()
		git := gitops.New(sealDir)

		name, url := args[0], args[1]
		if err := git.RemoteAdd(name, url); err != nil {
			return fmt.Errorf("adding remote: %w", err)
		}

		// Register merge driver. Embed the absolute binary path plus the source
		// and seal dir so git invokes the right store even without the shell
		// environment that ran this command (required for pi's separate store).
		driverCmd := mergeDriverCommand(sealDir)
		if err := git.ConfigMergeDriver("enclaude-manifest", driverCmd); err != nil {
			fmt.Printf("Warning: could not register merge driver: %v\n", err)
		}

		fmt.Printf("Remote '%s' added: %s\n", name, url)
		fmt.Println("Run 'enclaude push' to sync.")
		return nil
	},
}

var remoteListCmd = &cobra.Command{
	Use:   "list",
	Short: "List configured remotes",
	RunE: func(cmd *cobra.Command, args []string) error {
		git := gitops.New(getSealDir())
		out, err := git.RemoteList()
		if err != nil {
			return err
		}
		if out == "" {
			fmt.Println("No remotes configured.")
			fmt.Println("Run 'enclaude remote add origin <url>' to set up sync.")
		} else {
			fmt.Println(out)
		}
		return nil
	},
}

var remoteRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove a git remote",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		git := gitops.New(getSealDir())
		name := args[0]
		if err := git.RemoteRemove(name); err != nil {
			return fmt.Errorf("removing remote: %w", err)
		}
		fmt.Printf("Remote '%s' removed.\n", name)
		return nil
	},
}

var remoteEditCmd = &cobra.Command{
	Use:   "edit <name> <url>",
	Short: "Update the URL of an existing git remote",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		git := gitops.New(getSealDir())
		name, url := args[0], args[1]
		if err := git.RemoteSetURL(name, url); err != nil {
			return fmt.Errorf("editing remote: %w", err)
		}
		fmt.Printf("Remote '%s' updated: %s\n", name, url)
		return nil
	},
}

func init() {
	remoteCmd.AddCommand(remoteAddCmd)
	remoteCmd.AddCommand(remoteListCmd)
	remoteCmd.AddCommand(remoteRemoveCmd)
	remoteCmd.AddCommand(remoteEditCmd)
	rootCmd.AddCommand(remoteCmd)
}
