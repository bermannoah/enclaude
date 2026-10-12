package store

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/coredipper/enclaude/internal/config"
	"github.com/coredipper/enclaude/internal/crypto"
)

func newSecurityFixture(t *testing.T) (sourceDir, sealDir string) {
	t.Helper()
	root := t.TempDir()
	sourceDir = filepath.Join(root, "src")
	sealDir = filepath.Join(root, "seal")
	if err := os.MkdirAll(sourceDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sealDir, 0700); err != nil {
		t.Fatal(err)
	}
	return sourceDir, sealDir
}

func TestUnsealRefusesUnsafeManifestEntries(t *testing.T) {
	sourceDir, sealDir := newSecurityFixture(t)
	identity, _ := crypto.GenerateKey()
	cfg := config.DefaultConfigForSource(config.SourcePi, sourceDir, sealDir)

	cases := []struct {
		name string
		key  string
		hash string
	}{
		{"parent traversal", "../escape.txt", ContentHash([]byte("x"))},
		{"absolute path", "/tmp/escape.txt", ContentHash([]byte("x"))},
		{"invalid hash", "settings.json", "not-a-hash"},
		{"nested traversal", "a/../../escape.txt", ContentHash([]byte("x"))},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewManifest("attacker")
			m.Files[tc.key] = FileEntry{ContentHash: tc.hash}
			if err := m.Save(sealDir); err != nil {
				t.Fatal(err)
			}

			if _, err := Unseal(cfg, identity, UnsealOptions{AllowSensitiveResources: true}); err == nil {
				t.Fatal("expected Unseal to refuse an unsafe manifest entry")
			}

			// Nothing outside the source dir may be written.
			if _, err := os.Stat(filepath.Join(filepath.Dir(sourceDir), "escape.txt")); err == nil {
				t.Error("a file was written outside the source directory")
			}
		})
	}
}

func TestRepairDropsUnsafeManifestEntries(t *testing.T) {
	piDir := buildPiDir(t)
	sealDir := t.TempDir()
	identity, _ := crypto.GenerateKey()
	cfg := config.DefaultConfigForSource(config.SourcePi, piDir, sealDir)

	if _, err := Seal(cfg, identity.Recipient(), false, nil); err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(sealDir)
	if err != nil {
		t.Fatal(err)
	}
	m.Files["../escape.txt"] = FileEntry{ContentHash: ContentHash([]byte("boo"))}
	if err := m.Save(sealDir); err != nil {
		t.Fatal(err)
	}

	result, err := Repair(cfg, identity, false, false)
	if err != nil {
		t.Fatalf("Repair() error: %v", err)
	}
	if len(result.InvalidEntries) != 1 || result.InvalidEntries[0] != "../escape.txt" {
		t.Errorf("InvalidEntries = %v, want [../escape.txt]", result.InvalidEntries)
	}

	repaired, err := LoadManifest(sealDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := repaired.Files["../escape.txt"]; ok {
		t.Error("Repair did not drop the unsafe manifest entry")
	}
}

func TestUnsealGatesExecutableResourcesAndKeepsThemPending(t *testing.T) {
	piDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(piDir, "settings.json"), []byte(`{"defaultProvider":"x"}`), 0644); err != nil {
		t.Fatal(err)
	}
	sessionDir := filepath.Join(piDir, "sessions", "--proj--")
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "s.jsonl"), []byte(`{"type":"session","id":"s"}`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	sealDir := t.TempDir()
	identity, _ := crypto.GenerateKey()
	cfgSeal := config.DefaultConfigForSource(config.SourcePi, piDir, sealDir)
	if _, err := Seal(cfgSeal, identity.Recipient(), false, nil); err != nil {
		t.Fatal(err)
	}

	restoreDir := t.TempDir()
	cfgRestore := config.DefaultConfigForSource(config.SourcePi, restoreDir, sealDir)

	// Without --yes, executable resources are skipped, data is restored.
	stats, err := Unseal(cfgRestore, identity, UnsealOptions{})
	if err != nil {
		t.Fatalf("Unseal() error: %v", err)
	}
	if len(stats.SkippedSensitive) != 1 || stats.SkippedSensitive[0] != "settings.json" {
		t.Errorf("SkippedSensitive = %v, want [settings.json]", stats.SkippedSensitive)
	}
	if _, err := os.Stat(filepath.Join(restoreDir, "settings.json")); err == nil {
		t.Error("settings.json was written without --yes")
	}
	if _, err := os.Stat(filepath.Join(restoreDir, "sessions", "--proj--", "s.jsonl")); err != nil {
		t.Error("session data should have been restored")
	}

	// A subsequent seal must not treat the skipped file as a deletion.
	sealStats, err := Seal(cfgRestore, identity.Recipient(), false, nil)
	if err != nil {
		t.Fatalf("Seal() error: %v", err)
	}
	if sealStats.Deleted != 0 {
		t.Errorf("seal deleted %d files; pending executable should be protected", sealStats.Deleted)
	}
	manifest, err := LoadManifest(sealDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := manifest.Files["settings.json"]; !ok {
		t.Error("pending executable resource was dropped from the manifest")
	}

	// With --yes, the executable resource is applied and no longer pending.
	stats, err = Unseal(cfgRestore, identity, UnsealOptions{AllowSensitiveResources: true})
	if err != nil {
		t.Fatalf("Unseal(--yes) error: %v", err)
	}
	if len(stats.SkippedSensitive) != 0 {
		t.Errorf("SkippedSensitive = %v, want none", stats.SkippedSensitive)
	}
	if _, err := os.Stat(filepath.Join(restoreDir, "settings.json")); err != nil {
		t.Error("settings.json was not restored with --yes")
	}

	// Now that it exists, a seal keeps it and clears the pending flag.
	if _, err := Seal(cfgRestore, identity.Recipient(), false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(pendingFile(sealDir)); !os.IsNotExist(err) {
		t.Error("pending state should be cleared once the resource is applied")
	}
}

func TestUnsealGatesInstructionsAndRouting(t *testing.T) {
	piDir := t.TempDir()
	files := map[string]string{
		"settings.json":          `{}`,
		"SYSTEM.md":              "system prompt\n",
		"AGENTS.md":              "# instructions\n",
		"models.json":            `{"providers":{}}`,
		"sessions/--p--/s.jsonl": `{"type":"session","id":"s"}` + "\n",
	}
	for rel, content := range files {
		abs := filepath.Join(piDir, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	sealDir := t.TempDir()
	identity, _ := crypto.GenerateKey()
	cfgSeal := config.DefaultConfigForSource(config.SourcePi, piDir, sealDir)
	if _, err := Seal(cfgSeal, identity.Recipient(), false, nil); err != nil {
		t.Fatal(err)
	}

	restoreDir := t.TempDir()
	cfgRestore := config.DefaultConfigForSource(config.SourcePi, restoreDir, sealDir)
	stats, err := Unseal(cfgRestore, identity, UnsealOptions{})
	if err != nil {
		t.Fatalf("Unseal() error: %v", err)
	}

	got := append([]string(nil), stats.SkippedSensitive...)
	sort.Strings(got)
	want := []string{"AGENTS.md", "SYSTEM.md", "models.json", "settings.json"}
	if len(got) != len(want) {
		t.Fatalf("SkippedSensitive = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("SkippedSensitive[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	// Data is still restored.
	if _, err := os.Stat(filepath.Join(restoreDir, "sessions", "--p--", "s.jsonl")); err != nil {
		t.Error("session data should have been restored")
	}
	for _, rel := range []string{"SYSTEM.md", "AGENTS.md", "models.json"} {
		if _, err := os.Stat(filepath.Join(restoreDir, rel)); err == nil {
			t.Errorf("%s was written without --yes", rel)
		}
	}

	// Reasons are human readable.
	cases := map[string]string{
		"SYSTEM.md":       "steers the agent",
		"AGENTS.md":       "steers the agent",
		"models.json":     "changes request routing",
		"settings.json":   "runs code",
		"extensions/x.ts": "runs code",
	}
	for path, want := range cases {
		if got := SensitiveReason(path); got != want {
			t.Errorf("SensitiveReason(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestUnsealConfirmCannotBeDisabledFromConfig(t *testing.T) {
	piDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(piDir, "settings.json"), []byte(`{}`), 0644); err != nil {
		t.Fatal(err)
	}

	sealDir := t.TempDir()
	identity, _ := crypto.GenerateKey()
	cfgSeal := config.DefaultConfigForSource(config.SourcePi, piDir, sealDir)
	if _, err := Seal(cfgSeal, identity.Recipient(), false, nil); err != nil {
		t.Fatal(err)
	}

	restoreDir := t.TempDir()
	cfgRestore := config.DefaultConfigForSource(config.SourcePi, restoreDir, sealDir)
	cfgRestore.Confirm.Patterns = []string{} // a compromised remote tries this

	stats, err := Unseal(cfgRestore, identity, UnsealOptions{})
	if err != nil {
		t.Fatalf("Unseal() error: %v", err)
	}
	if len(stats.SkippedSensitive) != 1 || stats.SkippedSensitive[0] != "settings.json" {
		t.Errorf("SkippedSensitive = %v, want [settings.json] (config must not disable the gate)", stats.SkippedSensitive)
	}
	if _, err := os.Stat(filepath.Join(restoreDir, "settings.json")); err == nil {
		t.Error("settings.json was written even though the built-in gate should still apply")
	}
}

func TestUnsealConfirmCanAddPatterns(t *testing.T) {
	piDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(piDir, "notes.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	sealDir := t.TempDir()
	identity, _ := crypto.GenerateKey()
	cfgSeal := config.DefaultConfigForSource(config.SourcePi, piDir, sealDir)
	cfgSeal.Include.Patterns = append(cfgSeal.Include.Patterns, "notes.txt")
	if _, err := Seal(cfgSeal, identity.Recipient(), false, nil); err != nil {
		t.Fatal(err)
	}

	restoreDir := t.TempDir()
	cfgRestore := config.DefaultConfigForSource(config.SourcePi, restoreDir, sealDir)
	cfgRestore.Confirm.Patterns = []string{"notes.txt"} // additive

	stats, err := Unseal(cfgRestore, identity, UnsealOptions{})
	if err != nil {
		t.Fatalf("Unseal() error: %v", err)
	}
	if len(stats.SkippedSensitive) != 1 || stats.SkippedSensitive[0] != "notes.txt" {
		t.Errorf("SkippedSensitive = %v, want [notes.txt]", stats.SkippedSensitive)
	}
}

func TestUnsealBroadenedIncludeDoesNotDeleteUnmanaged(t *testing.T) {
	src := t.TempDir()
	for _, f := range []string{"a.txt", "b.txt", "secret.json"} {
		if err := os.WriteFile(filepath.Join(src, f), []byte(f), 0644); err != nil {
			t.Fatal(err)
		}
	}

	sealDir := t.TempDir()
	identity, _ := crypto.GenerateKey()
	cfg := config.DefaultConfigForSource(config.SourcePi, src, sealDir)
	cfg.Include.Patterns = []string{"a.txt"} // only a.txt is sealed/managed
	if _, err := Seal(cfg, identity.Recipient(), false, nil); err != nil {
		t.Fatal(err)
	}

	// Simulate a compromised remote rewriting seal.toml's include to "**".
	cfg.Include.Patterns = []string{"**"}
	cfg.Exclude.Patterns = nil
	if _, err := Unseal(cfg, identity, UnsealOptions{AllowSensitiveResources: true}); err != nil {
		t.Fatalf("Unseal() error: %v", err)
	}

	for _, f := range []string{"a.txt", "b.txt", "secret.json"} {
		if _, err := os.Stat(filepath.Join(src, f)); err != nil {
			t.Errorf("never-sealed %s was deleted by a broadened include: %v", f, err)
		}
	}
}

func TestUnsealRestoresExecutableBit(t *testing.T) {
	src := t.TempDir()
	script := filepath.Join(src, "tools", "run.sh")
	if err := os.MkdirAll(filepath.Dir(script), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}

	sealDir := t.TempDir()
	identity, _ := crypto.GenerateKey()
	cfg := config.DefaultConfigForSource(config.SourcePi, src, sealDir)
	if _, err := Seal(cfg, identity.Recipient(), false, nil); err != nil {
		t.Fatal(err)
	}

	restore := t.TempDir()
	cfg2 := config.DefaultConfigForSource(config.SourcePi, restore, sealDir)
	if _, err := Unseal(cfg2, identity, UnsealOptions{AllowSensitiveResources: true}); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(filepath.Join(restore, "tools", "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0755 {
		t.Errorf("restored mode = %o, want 755", info.Mode().Perm())
	}
}
