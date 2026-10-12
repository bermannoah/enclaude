package cmd

import (
	"fmt"
	"os"

	"github.com/coredipper/enclaude/internal/config"
	"github.com/spf13/cobra"
)

var Version = "0.3.0"

var (
	flagVerbose   bool
	flagDryRun    bool
	flagSource    string
	flagSourceDir string
	flagSealDir   string
)

var rootCmd = &cobra.Command{
	Use:   "enclaude",
	Short: "Encrypted git-like sync for AI coding agent data",
	Long: `enclaude provides age-encrypted, git-backed, JSONL-aware sync for your
Claude Code and pi.dev data. It encrypts conversation history, settings,
credentials, and memory at rest and syncs them across devices with version
history.`,
	Version: Version,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().BoolVarP(&flagVerbose, "verbose", "v", false, "verbose output")
	rootCmd.PersistentFlags().BoolVar(&flagDryRun, "dry-run", false, "show what would happen without doing it")
	rootCmd.PersistentFlags().StringVar(&flagSource, "source", "", "source profile: claude (default) or pi (env: ENCLAUDE_SOURCE)")
	rootCmd.PersistentFlags().StringVar(&flagSourceDir, "source-dir", "", "override the source directory (e.g. ~/.claude or ~/.pi/agent)")
	// Deprecated alias kept for backwards compatibility.
	rootCmd.PersistentFlags().StringVar(&flagSourceDir, "claude-dir", "", "alias for --source-dir")
	rootCmd.PersistentFlags().StringVar(&flagSealDir, "seal-dir", "", "override the seal store location")
}

// getSource resolves the active source profile from flags, the ENCLAUDE_SOURCE
// environment variable, or the Claude default.
func getSource() string {
	if flagSource != "" {
		return config.NormalizeSource(flagSource)
	}
	if env := os.Getenv("ENCLAUDE_SOURCE"); env != "" {
		return config.NormalizeSource(env)
	}
	return config.SourceClaude
}

// defaultSourceDir returns the conventional directory for a source profile.
// For pi, PI_CODING_AGENT_DIR is honoured, matching pi's own resolution.
func defaultSourceDir(source string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: cannot determine home directory:", err)
		os.Exit(1)
	}
	if source == config.SourcePi {
		if dir := os.Getenv("PI_CODING_AGENT_DIR"); dir != "" {
			return dir
		}
		return home + "/.pi/agent"
	}
	return home + "/.claude"
}

// defaultSealDir returns the conventional seal store for a source profile.
// Pi uses a distinct default so both agents can be sealed side by side.
func defaultSealDir(source string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: cannot determine home directory:", err)
		os.Exit(1)
	}
	if source == config.SourcePi {
		return home + "/.enclaude-pi"
	}
	return home + "/.enclaude"
}

// getSourceDir returns the configured source directory, honouring overrides.
func getSourceDir() string {
	if flagSourceDir != "" {
		return flagSourceDir
	}
	return defaultSourceDir(getSource())
}

func getSealDir() string {
	if flagSealDir != "" {
		return flagSealDir
	}
	return defaultSealDir(getSource())
}

// defaultSealDirCandidates lists the conventional seal stores. Used by key
// rotation so every store on the device is re-encrypted together (all stores
// share one keychain entry).
func defaultSealDirCandidates() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{home + "/.enclaude", home + "/.enclaude-pi"}
}

// otherExistingSealDirs returns seal stores in candidates (other than active)
// that are actually initialized. Candidates are passed in so the caller
// controls which locations are considered, which keeps tests off the real home
// directory.
func otherExistingSealDirs(active string, candidates []string) []string {
	var out []string
	for _, dir := range candidates {
		if dir == active {
			continue
		}
		if _, err := os.Stat(dir + "/seal.toml"); err == nil {
			out = append(out, dir)
		}
	}
	return out
}
