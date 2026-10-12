package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/coredipper/enclaude/internal/config"
)

// TestUpgradeMigratesLegacyClaudeConfig exercises the v2 -> v3 migration end to
// end: claude_dir becomes source_dir and the source field is set.
func TestUpgradeMigratesLegacyClaudeConfig(t *testing.T) {
	sealDir := t.TempDir()
	legacy := `config_version = 2
[seal]
claude_dir = "/tmp/legacy-claude"
seal_dir = "` + sealDir + `"
device_id = "legacy"
[merge_strategies]
"history.jsonl" = "jsonl_dedup"
`
	if err := os.WriteFile(filepath.Join(sealDir, "seal.toml"), []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}

	prev := flagSealDir
	flagSealDir = sealDir
	defer func() { flagSealDir = prev }()

	if err := runUpgrade(upgradeCmd, nil); err != nil {
		t.Fatalf("runUpgrade() error: %v", err)
	}

	cfg, err := config.Load(sealDir)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Version != config.ConfigVersion {
		t.Errorf("Version = %d, want %d", cfg.Version, config.ConfigVersion)
	}
	if cfg.Seal.SourceDir != "/tmp/legacy-claude" {
		t.Errorf("SourceDir = %q, want /tmp/legacy-claude", cfg.Seal.SourceDir)
	}
	if cfg.Source() != config.SourceClaude {
		t.Errorf("Source() = %q, want claude", cfg.Source())
	}
}
