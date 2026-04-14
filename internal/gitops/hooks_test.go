package gitops

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	// Override executable resolver so tests get a predictable command path
	resolveExecutable = func() (string, error) {
		return "/usr/local/bin/enclaude", nil
	}
	os.Exit(m.Run())
}

func TestInstallHooksPreservesExisting(t *testing.T) {
	dir := t.TempDir()

	// Write a settings.json with existing hooks (simulating peon-ping, notchi)
	existing := map[string]any{
		"env": map[string]any{
			"ENABLE_LSP_TOOL": "1",
		},
		"hooks": map[string]any{
			"SessionStart": []any{
				map[string]any{
					"matcher": "",
					"hooks": []any{
						map[string]any{
							"type":    "command",
							"command": "/path/to/peon-ping/peon.sh",
							"timeout": 10,
						},
					},
				},
			},
			"SessionEnd": []any{
				map[string]any{
					"matcher": "",
					"hooks": []any{
						map[string]any{
							"type":    "command",
							"command": "/path/to/peon-ping/peon.sh",
							"timeout": 10,
							"async":   true,
						},
					},
				},
			},
			"PreToolUse": []any{
				map[string]any{
					"matcher": "Bash",
					"hooks": []any{
						map[string]any{
							"type":    "command",
							"command": "/path/to/rtk-rewrite.sh",
						},
					},
				},
			},
		},
		"enabledPlugins": map[string]any{
			"linear@claude-plugins-official": true,
		},
	}

	data, _ := json.MarshalIndent(existing, "", "  ")
	os.WriteFile(filepath.Join(dir, "settings.json"), data, 0644)

	// Install hooks
	if err := InstallHooks(dir); err != nil {
		t.Fatalf("InstallHooks() error: %v", err)
	}

	// Read back
	result, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	resultStr := string(result)

	// Verify existing hooks preserved
	if !strings.Contains(resultStr, "peon-ping/peon.sh") {
		t.Error("peon-ping hook was removed")
	}
	if !strings.Contains(resultStr, "rtk-rewrite.sh") {
		t.Error("rtk-rewrite hook was removed")
	}

	// Verify seal hooks added with marker
	if !strings.Contains(resultStr, "hook-handler session-start") {
		t.Error("session-start hook not added")
	}
	if !strings.Contains(resultStr, "hook-handler session-end") {
		t.Error("session-end hook not added")
	}
	if strings.Count(resultStr, hookMarker) != 2 {
		t.Error("expected exactly 2 hook markers (SessionStart + SessionEnd)")
	}

	// Verify non-hook settings preserved
	if !strings.Contains(resultStr, "ENABLE_LSP_TOOL") {
		t.Error("env settings lost")
	}
	if !strings.Contains(resultStr, "enabledPlugins") {
		t.Error("enabledPlugins lost")
	}

	// Verify installed
	if !HooksInstalled(dir) {
		t.Error("HooksInstalled() returned false after install")
	}
}

func TestInstallHooksIdempotent(t *testing.T) {
	dir := t.TempDir()
	settings := map[string]any{"hooks": map[string]any{}}
	data, _ := json.MarshalIndent(settings, "", "  ")
	os.WriteFile(filepath.Join(dir, "settings.json"), data, 0644)

	// Install twice
	InstallHooks(dir)
	InstallHooks(dir)

	result, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	count := strings.Count(string(result), hookMarker)
	if count != 2 { // one per event (SessionStart + SessionEnd)
		t.Errorf("hook marker appears %d times, expected 2", count)
	}
}

func TestRemoveHooksPreservesOthers(t *testing.T) {
	dir := t.TempDir()
	settings := map[string]any{"hooks": map[string]any{}}
	data, _ := json.MarshalIndent(settings, "", "  ")
	os.WriteFile(filepath.Join(dir, "settings.json"), data, 0644)

	// Install then remove
	InstallHooks(dir)

	if !HooksInstalled(dir) {
		t.Fatal("hooks should be installed")
	}

	if err := RemoveHooks(dir); err != nil {
		t.Fatalf("RemoveHooks() error: %v", err)
	}

	if HooksInstalled(dir) {
		t.Error("hooks should be removed")
	}
}

func TestShellQuoteHandlesSpaces(t *testing.T) {
	old := resolveExecutable
	resolveExecutable = func() (string, error) {
		return "/path with spaces/enclaude", nil
	}
	defer func() { resolveExecutable = old }()

	cmd := sealHookCommand()
	if cmd != "'/path with spaces/enclaude' hook-handler" {
		t.Errorf("unexpected command: %s", cmd)
	}
}

func TestShellQuoteHandlesSingleQuotes(t *testing.T) {
	old := resolveExecutable
	resolveExecutable = func() (string, error) {
		return "/it's/enclaude", nil
	}
	defer func() { resolveExecutable = old }()

	cmd := sealHookCommand()
	expected := `'/it'\''s/enclaude' hook-handler`
	if cmd != expected {
		t.Errorf("unexpected command: got %s, want %s", cmd, expected)
	}
}

func TestSymlinkNotResolved(t *testing.T) {
	old := resolveExecutable
	resolveExecutable = func() (string, error) {
		return "/opt/homebrew/bin/enclaude", nil
	}
	defer func() { resolveExecutable = old }()

	cmd := sealHookCommand()
	if cmd != "'/opt/homebrew/bin/enclaude' hook-handler" {
		t.Errorf("unexpected command (symlink may have been resolved): %s", cmd)
	}
}

func TestContainsMarker(t *testing.T) {
	tests := []struct {
		cmd  string
		want bool
	}{
		// New-style: marker comment present
		{"enclaude hook-handler session-start  " + hookMarker, true},
		{"'/usr/local/bin/enclaude' hook-handler session-end  " + hookMarker, true},
		{"'/path with spaces/enclaude' hook-handler session-start  " + hookMarker, true},
		// Legacy: no marker but contains "enclaude hook-handler"
		{"enclaude hook-handler session-start", true},
		{"'/usr/local/bin/enclaude' hook-handler session-end", true},
		{"/Users/bogdan/go/bin/enclaude hook-handler session-start", true},
		// Should NOT match
		{"/path/to/peon-ping/peon.sh", false},
		{"some-random-script --foo", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := containsMarker(tt.cmd); got != tt.want {
			t.Errorf("containsMarker(%q) = %v, want %v", tt.cmd, got, tt.want)
		}
	}
}

func TestSealHookFullIncludesMarker(t *testing.T) {
	cmd := sealHookFull("session-start")
	if !strings.Contains(cmd, hookMarker) {
		t.Errorf("sealHookFull missing marker: %s", cmd)
	}
	if !strings.Contains(cmd, "session-start") {
		t.Errorf("sealHookFull missing action: %s", cmd)
	}
	if !strings.Contains(cmd, "/usr/local/bin/enclaude") {
		t.Errorf("sealHookFull missing executable path: %s", cmd)
	}
}

func TestHooksInstalledFalseWhenMissing(t *testing.T) {
	dir := t.TempDir()
	if HooksInstalled(dir) {
		t.Error("should return false when no settings.json")
	}
}
