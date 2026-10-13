package cmd

import (
	"testing"

	"github.com/coredipper/enclaude/internal/config"
)

// saveSourceFlags snapshots and restores the globals that resolveSourceDir reads.
func saveSourceFlags(t *testing.T) {
	t.Helper()
	prevDir, prevSource, prevSeal := flagSourceDir, flagSource, flagSealDir
	t.Cleanup(func() { flagSourceDir, flagSource, flagSealDir = prevDir, prevSource, prevSeal })
	t.Setenv("ENCLAUDE_SOURCE", "")
}

// TestResolveSourceDirRequiresFlagForCustomDir verifies H2: a non-default
// source_dir in the (synced) seal.toml is not trusted unless confirmed locally
// with --source-dir.
func TestResolveSourceDirRequiresFlagForCustomDir(t *testing.T) {
	saveSourceFlags(t)
	flagSourceDir, flagSource, flagSealDir = "", "", ""

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
// no source-dir flag when the locally selected source matches the store.
func TestResolveSourceDirAllowsLocalDefault(t *testing.T) {
	saveSourceFlags(t)
	t.Setenv("PI_CODING_AGENT_DIR", "/tmp/env-pi-dir")
	flagSourceDir, flagSource, flagSealDir = "", "pi", ""

	cfg := config.DefaultConfigForSource(config.SourcePi, "/tmp/env-pi-dir", "/tmp/seal")
	dir, err := resolveSourceDir(cfg)
	if err != nil {
		t.Fatalf("resolveSourceDir() error: %v", err)
	}
	if dir != "/tmp/env-pi-dir" {
		t.Errorf("dir = %q, want /tmp/env-pi-dir", dir)
	}
}

// TestResolveSourceDirRejectsProfileMismatch verifies R3: the synced source
// field cannot switch the local profile.
func TestResolveSourceDirRejectsProfileMismatch(t *testing.T) {
	saveSourceFlags(t)
	flagSourceDir, flagSource, flagSealDir = "", "claude", ""

	cfg := config.DefaultConfigForSource(config.SourcePi, "/tmp/env-pi-dir", "/tmp/seal")
	if _, err := resolveSourceDir(cfg); err == nil {
		t.Fatal("expected an error when the store's source disagrees with the local profile")
	}
}

// TestResolveSourceDirAcceptsConfigProfileWithExplicitSealDir verifies B4: when
// the user explicitly points at a store with --seal-dir and names no profile,
// the store's own profile is trusted for its default directory.
func TestResolveSourceDirAcceptsConfigProfileWithExplicitSealDir(t *testing.T) {
	saveSourceFlags(t)
	t.Setenv("PI_CODING_AGENT_DIR", "/tmp/env-pi-dir")
	flagSourceDir, flagSource, flagSealDir = "", "", "/tmp/some-seal-dir"

	cfg := config.DefaultConfigForSource(config.SourcePi, "/tmp/env-pi-dir", "/tmp/seal")
	dir, err := resolveSourceDir(cfg)
	if err != nil {
		t.Fatalf("resolveSourceDir() error: %v", err)
	}
	if dir != "/tmp/env-pi-dir" {
		t.Errorf("dir = %q, want /tmp/env-pi-dir", dir)
	}
}
