package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(t.TempDir())
	if err == nil {
		t.Fatal("expected error for missing seal.toml, got nil")
	}
}

func TestLoadOverlaysAllDefaultsWhenMergeSectionAbsent(t *testing.T) {
	sealDir := t.TempDir()
	content := `config_version = 2
[seal]
claude_dir = "/tmp/claude"
seal_dir = "/tmp/seal"
device_id = "test-device"
`
	if err := os.WriteFile(filepath.Join(sealDir, "seal.toml"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(sealDir)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Merge == nil {
		t.Fatal("expected Merge map after overlay, got nil")
	}
	if _, ok := cfg.Merge["history.jsonl"]; !ok {
		t.Error("expected history.jsonl to be injected from defaults")
	}
	if _, ok := cfg.Merge["projects/*/sessions-index.json"]; !ok {
		t.Error("expected projects/*/sessions-index.json to be injected")
	}
}

func TestLoadPreservesUserOverride(t *testing.T) {
	sealDir := t.TempDir()
	content := `config_version = 2
[seal]
claude_dir = "/tmp/claude"
seal_dir = "/tmp/seal"
device_id = "test-device"

[merge_strategies]
"history.jsonl" = "last_write_wins"
`
	if err := os.WriteFile(filepath.Join(sealDir, "seal.toml"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(sealDir)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Merge["history.jsonl"] != "last_write_wins" {
		t.Errorf("user override should be preserved, got %q", cfg.Merge["history.jsonl"])
	}
}

func TestLoadOverlaysOnlyMissingKeys(t *testing.T) {
	sealDir := t.TempDir()
	content := `config_version = 2
[seal]
claude_dir = "/tmp/claude"
seal_dir = "/tmp/seal"
device_id = "test-device"

[merge_strategies]
"history.jsonl" = "last_write_wins"
`
	if err := os.WriteFile(filepath.Join(sealDir, "seal.toml"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(sealDir)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Merge["history.jsonl"] != "last_write_wins" {
		t.Errorf("user key should be preserved, got %q", cfg.Merge["history.jsonl"])
	}
	if _, ok := cfg.Merge["projects/*/sessions-index.json"]; !ok {
		t.Error("missing default key should be injected alongside user key")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	sealDir := t.TempDir()
	orig := DefaultConfig("/tmp/claude", sealDir)
	orig.Seal.DeviceID = "roundtrip-test-device"

	if err := orig.Save(sealDir); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	loaded, err := Load(sealDir)
	if err != nil {
		t.Fatalf("Load() after Save() error: %v", err)
	}
	if loaded.Seal.DeviceID != orig.Seal.DeviceID {
		t.Errorf("DeviceID: got %q, want %q", loaded.Seal.DeviceID, orig.Seal.DeviceID)
	}
	if loaded.Version != orig.Version {
		t.Errorf("Version: got %d, want %d", loaded.Version, orig.Version)
	}
	if len(loaded.Merge) < len(orig.Merge) {
		t.Errorf("merge strategies lost: got %d, want %d", len(loaded.Merge), len(orig.Merge))
	}
}

func TestNormalizeSource(t *testing.T) {
	cases := map[string]string{
		"":                  SourceClaude,
		"claude":            SourceClaude,
		"Claude":            SourceClaude,
		"claude-code":       SourceClaude,
		"pi":                SourcePi,
		"PI":                SourcePi,
		"pi.dev":            SourcePi,
		"pi-coding-agent":   SourcePi,
		"something-unknown": SourceClaude,
	}
	for in, want := range cases {
		if got := NormalizeSource(in); got != want {
			t.Errorf("NormalizeSource(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDefaultConfigForSourcePi(t *testing.T) {
	cfg := DefaultConfigForSource("pi", "/tmp/pi-agent", "/tmp/seal")

	if cfg.Source() != SourcePi {
		t.Fatalf("Source() = %q, want %q", cfg.Source(), SourcePi)
	}
	if cfg.Seal.SourceDir != "/tmp/pi-agent" {
		t.Errorf("SourceDir = %q, want /tmp/pi-agent", cfg.Seal.SourceDir)
	}
	if cfg.Merge["sessions/**/*.jsonl"] != "immutable" {
		t.Errorf("pi sessions should be immutable, got %q", cfg.Merge["sessions/**/*.jsonl"])
	}
	if cfg.Merge["settings.json"] != "last_write_wins" {
		t.Errorf("pi settings.json should be last_write_wins, got %q", cfg.Merge["settings.json"])
	}
	// Claude-only defaults must not leak in.
	if _, ok := cfg.Merge["history.jsonl"]; ok {
		t.Error("pi config should not contain Claude's history.jsonl strategy")
	}

	var hasSessionsInclude, hasBinExclude bool
	for _, p := range cfg.Include.Patterns {
		if p == "sessions/**/*.jsonl" {
			hasSessionsInclude = true
		}
	}
	for _, p := range cfg.Exclude.Patterns {
		if p == "bin/**" {
			hasBinExclude = true
		}
	}
	if !hasSessionsInclude {
		t.Error("pi include patterns should contain sessions/**/*.jsonl")
	}
	if !hasBinExclude {
		t.Error("pi exclude patterns should contain bin/**")
	}
}

func TestLoadMigratesLegacyClaudeDir(t *testing.T) {
	sealDir := t.TempDir()
	content := `config_version = 2
[seal]
claude_dir = "/tmp/legacy-claude"
seal_dir = "/tmp/seal"
device_id = "test-device"
`
	if err := os.WriteFile(filepath.Join(sealDir, "seal.toml"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(sealDir)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Seal.SourceDir != "/tmp/legacy-claude" {
		t.Errorf("SourceDir = %q, want /tmp/legacy-claude", cfg.Seal.SourceDir)
	}
	if cfg.Seal.LegacyClaudeDir != "" {
		t.Errorf("LegacyClaudeDir should be cleared, got %q", cfg.Seal.LegacyClaudeDir)
	}
	if cfg.Source() != SourceClaude {
		t.Errorf("Source() = %q, want claude", cfg.Source())
	}
}

func TestLoadPiConfigOverlaysPiDefaults(t *testing.T) {
	sealDir := t.TempDir()
	content := `config_version = 3
[seal]
source = "pi"
source_dir = "/tmp/pi"
seal_dir = "/tmp/seal"
device_id = "test-device"

[merge_strategies]
"settings.json" = "text_merge"
`
	if err := os.WriteFile(filepath.Join(sealDir, "seal.toml"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(sealDir)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Merge["settings.json"] != "text_merge" {
		t.Errorf("user override not preserved: %q", cfg.Merge["settings.json"])
	}
	if cfg.Merge["sessions/**/*.jsonl"] != "immutable" {
		t.Errorf("missing pi default sessions strategy: %q", cfg.Merge["sessions/**/*.jsonl"])
	}
	if _, ok := cfg.Merge["history.jsonl"]; ok {
		t.Error("claude history strategy leaked into pi config")
	}
	if len(cfg.Include.Patterns) == 0 {
		t.Error("expected pi include defaults to be overlaid")
	}
}

func TestSaveDoesNotWriteLegacyClaudeDir(t *testing.T) {
	sealDir := t.TempDir()
	cfg := DefaultConfigForSource("pi", "/tmp/pi", sealDir)
	if err := cfg.Save(sealDir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(sealDir, "seal.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "claude_dir") {
		t.Errorf("saved config should not contain claude_dir:\n%s", data)
	}
	if !strings.Contains(string(data), "source_dir") {
		t.Errorf("saved config should contain source_dir:\n%s", data)
	}
	if !strings.Contains(string(data), "source = 'pi'") {
		t.Errorf("saved config should contain source = 'pi':\n%s", data)
	}
}

func TestLoadPinsSealDirToLoadPath(t *testing.T) {
	sealDir := t.TempDir()
	cfg := DefaultConfigForSource("pi", "/tmp/pi", sealDir)
	// Simulate a store relocated from another machine: the stored seal_dir is
	// stale, but we load from the new location.
	cfg.Seal.SealDir = "/somewhere/else"
	if err := cfg.Save(sealDir); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(sealDir)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if loaded.Seal.SealDir != sealDir {
		t.Errorf("SealDir = %q, want the load path %q", loaded.Seal.SealDir, sealDir)
	}
}

func TestDefaultConfirmPatterns(t *testing.T) {
	cfg := DefaultConfigForSource("pi", "/tmp/pi", "/tmp/seal")
	for _, want := range []string{
		"settings.json",
		"extensions/**",
		"SYSTEM.md",
		"APPEND_SYSTEM.md",
		"AGENTS.md",
		"AGENTS.override.md",
		"CLAUDE.md",
		"models.json",
	} {
		found := false
		for _, p := range cfg.Confirm.Patterns {
			if p == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("default confirm patterns missing %q", want)
		}
	}
}

func TestLoadOverlaysConfirmDefaults(t *testing.T) {
	sealDir := t.TempDir()
	content := `config_version = 3
[seal]
source = "pi"
source_dir = "/tmp/pi"
seal_dir = "/tmp/seal"
device_id = "test-device"
`
	if err := os.WriteFile(filepath.Join(sealDir, "seal.toml"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(sealDir)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(cfg.Confirm.Patterns) == 0 {
		t.Fatal("expected confirm defaults to be overlaid")
	}
}

func TestLoadConfirmEmptyDisables(t *testing.T) {
	sealDir := t.TempDir()
	content := `config_version = 4
[seal]
source = "pi"
source_dir = "/tmp/pi"
seal_dir = "/tmp/seal"
device_id = "test-device"

[confirm]
patterns = []
`
	if err := os.WriteFile(filepath.Join(sealDir, "seal.toml"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(sealDir)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Confirm.Patterns == nil {
		t.Fatal("explicit empty [confirm] patterns should stay non-nil (gating disabled)")
	}
	if len(cfg.Confirm.Patterns) != 0 {
		t.Errorf("expected no confirm patterns, got %v", cfg.Confirm.Patterns)
	}
}
