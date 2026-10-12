package store

import (
	"os"
	"path/filepath"
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

			if _, err := Unseal(cfg, identity, UnsealOptions{AllowExecutableResources: true}); err == nil {
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
	if len(stats.SkippedExecutable) != 1 || stats.SkippedExecutable[0] != "settings.json" {
		t.Errorf("SkippedExecutable = %v, want [settings.json]", stats.SkippedExecutable)
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
	stats, err = Unseal(cfgRestore, identity, UnsealOptions{AllowExecutableResources: true})
	if err != nil {
		t.Fatalf("Unseal(--yes) error: %v", err)
	}
	if len(stats.SkippedExecutable) != 0 {
		t.Errorf("SkippedExecutable = %v, want none", stats.SkippedExecutable)
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
