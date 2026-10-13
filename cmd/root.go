package cmd

import (
	"fmt"
	"os"
	"path/filepath"

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
	flagYes       bool
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
	rootCmd.PersistentFlags().BoolVar(&flagYes, "yes", false, "allow unseal to write sensitive resources (code, instructions, request routing)")
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

// resolveSourceDir returns the directory that seal/unseal should operate on.
//
// Both the source profile and a non-default source_dir may only be trusted from
// local state (flags, environment, or the built-in default), never from the
// synced seal.toml. This prevents a compromised remote from redirecting
// operations to another directory or another agent's default.
//
// One exception: if the user explicitly points at a store with --seal-dir and
// does not name a profile, that store's own profile is trusted for its default
// directory (so e.g. `hooks status --seal-dir ~/.enclaude-pi` works).
func resolveSourceDir(cfg *config.Config) (string, error) {
	if flagSourceDir != "" {
		return flagSourceDir, nil
	}
	if local := getSource(); cfg.Source() != local {
		if flagSealDir == "" || sourceExplicitlySet() {
			return "", fmt.Errorf("seal.toml records source %q but this command is using %q; pass --source %s (or --source-dir) to confirm",
				cfg.Source(), local, cfg.Source())
		}
	}
	def := defaultSourceDir(cfg.Source())
	if cfg.Seal.SourceDir != "" && filepath.Clean(cfg.Seal.SourceDir) != filepath.Clean(def) {
		return "", fmt.Errorf("seal.toml records source_dir %q, which differs from the local default %q; pass --source-dir %q to confirm",
			cfg.Seal.SourceDir, def, cfg.Seal.SourceDir)
	}
	return def, nil
}

// sourceExplicitlySet reports whether the user named a source profile rather
// than relying on the default.
func sourceExplicitlySet() bool {
	return flagSource != "" || os.Getenv("ENCLAUDE_SOURCE") != ""
}

func getSealDir() string {
	if flagSealDir != "" {
		return flagSealDir
	}
	return defaultSealDir(getSource())
}
