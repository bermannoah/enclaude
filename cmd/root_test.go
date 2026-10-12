package cmd

import (
	"testing"

	"github.com/coredipper/enclaude/internal/config"
)

// TestResolveSourceDirRequiresFlagForCustomDir verifies H2: a non-default
// source_dir in the (synced) seal.toml is not trusted unless confirmed locally
// with --source-dir.
func TestResolveSourceDirRequiresFlagForCustomDir(t *testing.T) {
	prev := flagSourceDir
	flagSourceDir = ""
	defer func() { flagSourceDir = prev }()

	cfg := config.DefaultConfigForSource(config.SourcePi, "/tmp/custom-pi-dir", "/tmp/seal")

	if _, err := resolveSourceDir(cfg); err == nil {
		t.Fatal("expected an error for an unconfirmed non-default source_dir")
	}

	flagSourceDir = "/tmp/custom-pi-dir"
	dir, err := resolveSourceDir(cfg)
	if err != nil {
		t.Fatalf("resolveSourceDir() error: %v", err)
	}
	if dir != "/tmp/custom-pi-dir" {
		t.Errorf("dir = %q, want /tmp/custom-pi-dir", dir)
	}
}

// TestResolveSourceDirAllowsLocalDefault verifies that a default install needs
// no flag and ignores whatever the config records as long as it matches.
func TestResolveSourceDirAllowsLocalDefault(t *testing.T) {
	t.Setenv("PI_CODING_AGENT_DIR", "/tmp/env-pi-dir")
	prev := flagSourceDir
	flagSourceDir = ""
	defer func() { flagSourceDir = prev }()

	cfg := config.DefaultConfigForSource(config.SourcePi, "/tmp/env-pi-dir", "/tmp/seal")
	dir, err := resolveSourceDir(cfg)
	if err != nil {
		t.Fatalf("resolveSourceDir() error: %v", err)
	}
	if dir != "/tmp/env-pi-dir" {
		t.Errorf("dir = %q, want /tmp/env-pi-dir", dir)
	}
}
