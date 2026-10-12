package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/coredipper/enclaude/internal/config"
	"github.com/coredipper/enclaude/internal/crypto"
)

// buildPiDir creates a representative ~/.pi/agent tree.
func buildPiDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	files := map[string]string{
		"settings.json":          `{"defaultProvider":"deepseek"}`,
		"models.json":            `{"providers":{}}`,
		"models-store.json":      `{"deepseek":{"models":[]}}`,
		"keybindings.json":       `{}`,
		"mcp.json":               `{"mcpServers":{}}`,
		"AGENTS.md":              "# user instructions\n",
		"extensions/hello.ts":    "export default function () {}\n",
		"skills/review/SKILL.md": "# review\n",
		"prompts/summarize.md":   "# summarize\n",
		"themes/dark.json":       `{"name":"dark"}`,
		"sessions/--Users-aura-code-demo--/2026-01-01T00-00-00-000Z_abc.jsonl": `{"type":"session","version":3,"id":"abc"}` + "\n",
		// Excluded noise.
		"bin/rg":                        "binary data",
		"npm/node_modules/pkg/index.js": "module.exports = {}",
		"mcp.log":                       "log line",
		"crashes.json":                  "{}",
		".models.json.swp":              "vim swap",
		// The enclaude hook extension is device-local and must not be sealed.
		"extensions/enclaude.ts": "// enclaude-managed\n",
		// Credentials and trust decisions are device-local and must not be sealed.
		"auth.json":     `{"deepseek":{"type":"api_key"}}`,
		"oauth.json":    `{}`,
		"mcp-auth.json": `{}`,
		"trust.json":    `{}`,
	}

	for rel, content := range files {
		abs := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestPiSealUnsealRoundTrip(t *testing.T) {
	piDir := buildPiDir(t)
	sealDir := t.TempDir()

	identity, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey() error: %v", err)
	}
	cfg := config.DefaultConfigForSource(config.SourcePi, piDir, sealDir)

	stats, err := Seal(cfg, identity.Recipient(), false, nil)
	if err != nil {
		t.Fatalf("Seal() error: %v", err)
	}
	if stats.Errors != 0 {
		t.Fatalf("Seal() reported %d errors", stats.Errors)
	}

	manifest, err := LoadManifest(sealDir)
	if err != nil {
		t.Fatalf("LoadManifest() error: %v", err)
	}

	included := []string{
		"settings.json",
		"models.json",
		"AGENTS.md",
		"extensions/hello.ts",
		"skills/review/SKILL.md",
		"sessions/--Users-aura-code-demo--/2026-01-01T00-00-00-000Z_abc.jsonl",
	}
	for _, rel := range included {
		if _, ok := manifest.Files[rel]; !ok {
			t.Errorf("expected %s to be sealed", rel)
		}
	}

	excluded := []string{
		"bin/rg",
		"npm/node_modules/pkg/index.js",
		"mcp.log",
		"crashes.json",
		".models.json.swp",
		"extensions/enclaude.ts",
		"auth.json",
		"oauth.json",
		"mcp-auth.json",
		"trust.json",
	}
	for _, rel := range excluded {
		if _, ok := manifest.Files[rel]; ok {
			t.Errorf("expected %s to be excluded from the seal", rel)
		}
	}

	// pi sessions must use the immutable union strategy.
	sessEntry := manifest.Files["sessions/--Users-aura-code-demo--/2026-01-01T00-00-00-000Z_abc.jsonl"]
	if sessEntry.MergeStrategy != "immutable" {
		t.Errorf("session strategy = %q, want immutable", sessEntry.MergeStrategy)
	}
	if !sessEntry.SessionComplete {
		t.Error("pi session should be marked complete")
	}

	// Round-trip into a fresh directory.
	restoreDir := t.TempDir()
	cfg2 := config.DefaultConfigForSource(config.SourcePi, restoreDir, sealDir)
	unsealStats, err := Unseal(cfg2, identity, UnsealOptions{AllowSensitiveResources: true})
	if err != nil {
		t.Fatalf("Unseal() error: %v", err)
	}
	if unsealStats.Errors != 0 {
		t.Fatalf("Unseal() reported %d errors", unsealStats.Errors)
	}

	for rel, entry := range manifest.Files {
		data, err := os.ReadFile(filepath.Join(restoreDir, rel))
		if err != nil {
			t.Errorf("cannot read restored %s: %v", rel, err)
			continue
		}
		if ContentHash(data) != entry.ContentHash {
			t.Errorf("restored hash mismatch for %s", rel)
		}
	}
}

func TestPiSealIdempotent(t *testing.T) {
	piDir := buildPiDir(t)
	sealDir := t.TempDir()

	identity, _ := crypto.GenerateKey()
	cfg := config.DefaultConfigForSource(config.SourcePi, piDir, sealDir)

	first, err := Seal(cfg, identity.Recipient(), false, nil)
	if err != nil {
		t.Fatalf("first Seal() error: %v", err)
	}
	second, err := Seal(cfg, identity.Recipient(), false, nil)
	if err != nil {
		t.Fatalf("second Seal() error: %v", err)
	}
	if second.Added != 0 || second.Modified != 0 {
		t.Errorf("second seal should be a no-op, got added=%d modified=%d", second.Added, second.Modified)
	}
	if second.Unchanged != first.Added {
		t.Errorf("unchanged = %d, want %d", second.Unchanged, first.Added)
	}
}

func TestSealNoChangesDoesNotRewriteManifest(t *testing.T) {
	piDir := buildPiDir(t)
	sealDir := t.TempDir()

	identity, _ := crypto.GenerateKey()
	cfg := config.DefaultConfigForSource(config.SourcePi, piDir, sealDir)

	if _, err := Seal(cfg, identity.Recipient(), false, nil); err != nil {
		t.Fatalf("first Seal() error: %v", err)
	}
	before, err := os.ReadFile(filepath.Join(sealDir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}

	// A no-op seal must not touch the manifest. Otherwise every seal leaves
	// the store dirty and a later `git pull` refuses to merge over it.
	if _, err := Seal(cfg, identity.Recipient(), false, nil); err != nil {
		t.Fatalf("second Seal() error: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(sealDir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("manifest changed on a no-op seal")
	}
}

func TestPiMergeStrategyResolution(t *testing.T) {
	cfg := config.DefaultConfigForSource(config.SourcePi, "/tmp/pi", "/tmp/seal")

	cases := []struct {
		path string
		want string
	}{
		{"settings.json", "last_write_wins"},
		{"models.json", "last_write_wins"},
		{"models-store.json", "last_write_wins"},
		{"sessions/--Users-aura-code-proj--/2026-01-01T00-00-00-000Z_abc.jsonl", "immutable"},
		{"extensions/hello.ts", "text_merge"},
		{"extensions/nested/index.js", "text_merge"},
		{"skills/review/SKILL.md", "text_merge"},
		{"prompts/summarize.md", "text_merge"},
		{"AGENTS.md", "text_merge"},
		{"themes/dark.json", "last_write_wins"},
	}
	for _, tc := range cases {
		if got := ResolveMergeStrategy(tc.path, cfg.Merge); got != tc.want {
			t.Errorf("ResolveMergeStrategy(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}
