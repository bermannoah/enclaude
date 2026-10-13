package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/coredipper/enclaude/internal/config"
	"github.com/coredipper/enclaude/internal/crypto"
)

func TestUnsealGatesSensitiveResources(t *testing.T) {
	piDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(piDir, "sessions", "--p--"), 0755); err != nil {
		t.Fatal(err)
	}
	for rel, content := range map[string]string{
		"settings.json":          `{"a":1}`,
		"SYSTEM.md":              "sys\n",
		"sessions/--p--/s.jsonl": `{"type":"session","id":"s"}` + "\n",
	} {
		if err := os.WriteFile(filepath.Join(piDir, rel), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	sealDir := t.TempDir()
	id, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	cfgSeal := config.DefaultConfigForSource(config.SourcePi, piDir, sealDir)
	if _, err := Seal(cfgSeal, id.Recipient(), false, nil); err != nil {
		t.Fatal(err)
	}

	restore := t.TempDir()
	cfgRestore := config.DefaultConfigForSource(config.SourcePi, restore, sealDir)
	stats, err := Unseal(cfgRestore, id, false, nil)
	if err != nil {
		t.Fatalf("Unseal() error: %v", err)
	}

	for _, p := range []string{"settings.json", "SYSTEM.md"} {
		if _, err := os.Stat(filepath.Join(restore, p)); err == nil {
			t.Errorf("%s was written without WithAllowSensitive", p)
		}
	}
	if _, err := os.Stat(filepath.Join(restore, "sessions", "--p--", "s.jsonl")); err != nil {
		t.Error("non-sensitive session data should have been restored")
	}
	if len(stats.SkippedSensitive) != 2 {
		t.Errorf("SkippedSensitive = %v, want 2 entries", stats.SkippedSensitive)
	}

	// A later seal must not treat the skipped entries as deletions.
	if _, err := Seal(cfgRestore, id.Recipient(), false, nil); err != nil {
		t.Fatal(err)
	}
	manifest, err := LoadManifest(sealDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := manifest.Files["settings.json"]; !ok {
		t.Error("pending sensitive entry was dropped from the manifest")
	}

	// With allow, they are applied.
	if _, err := Unseal(cfgRestore, id, false, nil, WithAllowSensitive(true)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(restore, "settings.json")); err != nil {
		t.Error("settings.json was not applied with WithAllowSensitive")
	}
}

func TestUnsealGatesCredentialInjection(t *testing.T) {
	sealDir := t.TempDir()
	src := t.TempDir()
	id, _ := crypto.GenerateKey()

	st := NewObjectStore(sealDir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	m := NewManifest("attacker")
	for _, p := range []string{"auth.json", "oauth.json", "mcp-auth.json", "trust.json"} {
		c := []byte(`{"attacker":true}`)
		h := ContentHash(c)
		enc, _ := crypto.Encrypt(c, id.Recipient())
		if err := st.Write(h, enc); err != nil {
			t.Fatal(err)
		}
		m.Files[p] = FileEntry{ContentHash: h}
	}
	if err := m.Save(sealDir); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfigForSource(config.SourcePi, src, sealDir)
	stats, err := Unseal(cfg, id, false, nil)
	if err != nil {
		t.Fatalf("Unseal() error: %v", err)
	}
	if len(stats.SkippedSensitive) != 4 {
		t.Errorf("SkippedSensitive = %v, want 4", stats.SkippedSensitive)
	}
	for _, p := range []string{"auth.json", "oauth.json", "mcp-auth.json", "trust.json"} {
		if _, err := os.Stat(filepath.Join(src, p)); err == nil {
			t.Errorf("%s was injected without WithAllowSensitive", p)
		}
	}
}

func TestUnsealRestoresMode(t *testing.T) {
	piDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(piDir, "tools"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(piDir, "tools", "run.sh"), []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}

	sealDir := t.TempDir()
	id, _ := crypto.GenerateKey()
	cfg := config.DefaultConfigForSource(config.SourcePi, piDir, sealDir)
	if _, err := Seal(cfg, id.Recipient(), false, nil); err != nil {
		t.Fatal(err)
	}

	restore := t.TempDir()
	cfg2 := config.DefaultConfigForSource(config.SourcePi, restore, sealDir)
	if _, err := Unseal(cfg2, id, false, nil, WithAllowSensitive(true)); err != nil {
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

func TestUnsealBroadenedIncludeDoesNotDeleteUnmanaged(t *testing.T) {
	src := t.TempDir()
	for _, f := range []string{"a.txt", "secret.json"} {
		if err := os.WriteFile(filepath.Join(src, f), []byte(f), 0644); err != nil {
			t.Fatal(err)
		}
	}

	sealDir := t.TempDir()
	id, _ := crypto.GenerateKey()
	cfg := config.DefaultConfigForSource(config.SourcePi, src, sealDir)
	cfg.Include.Patterns = []string{"a.txt"}
	if _, err := Seal(cfg, id.Recipient(), false, nil); err != nil {
		t.Fatal(err)
	}

	cfg.Include.Patterns = []string{"**"}
	if _, err := Unseal(cfg, id, false, nil, WithAllowSensitive(true)); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"a.txt", "secret.json"} {
		if _, err := os.Stat(filepath.Join(src, f)); err != nil {
			t.Errorf("%s was deleted by a broadened include: %v", f, err)
		}
	}
}
