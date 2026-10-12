package gitops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallPiHooksCreatesExtension(t *testing.T) {
	dir := t.TempDir()

	if PiHooksInstalled(dir) {
		t.Fatal("PiHooksInstalled() = true before install")
	}
	if err := InstallPiHooks(dir, "/tmp/seal"); err != nil {
		t.Fatalf("InstallPiHooks() error: %v", err)
	}
	if !PiHooksInstalled(dir) {
		t.Fatal("PiHooksInstalled() = false after install")
	}

	data, err := os.ReadFile(PiExtensionPath(dir))
	if err != nil {
		t.Fatalf("reading extension: %v", err)
	}
	src := string(data)
	for _, want := range []string{
		PiHookMarker,
		`"session_start"`,
		`"session_shutdown"`,
		`"hook-handler"`,
		`"session-start"`,
		`"session-end"`,
		`"--source"`,
		`"--seal-dir"`,
		`"/tmp/seal"`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("extension missing %q", want)
		}
	}
	// The synced extension must resolve the binary at runtime so it works
	// across machines with different install paths.
	for _, want := range []string{"process.env.ENCLAUDE_BIN", "existsSync"} {
		if !strings.Contains(src, want) {
			t.Errorf("extension should resolve the binary at runtime (missing %q)", want)
		}
	}
}

func TestInstallPiHooksPreservesOtherExtensions(t *testing.T) {
	dir := t.TempDir()
	extDir := filepath.Join(dir, "extensions")
	if err := os.MkdirAll(extDir, 0755); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(extDir, "mine.ts")
	if err := os.WriteFile(other, []byte("export default function () {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := InstallPiHooks(dir, "/tmp/seal"); err != nil {
		t.Fatalf("InstallPiHooks() error: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("other extension was removed: %v", err)
	}
}

func TestRemovePiHooks(t *testing.T) {
	dir := t.TempDir()
	if err := InstallPiHooks(dir, "/tmp/seal"); err != nil {
		t.Fatal(err)
	}
	if err := RemovePiHooks(dir); err != nil {
		t.Fatalf("RemovePiHooks() error: %v", err)
	}
	if PiHooksInstalled(dir) {
		t.Error("extension still installed after remove")
	}
	// Removing again is a no-op.
	if err := RemovePiHooks(dir); err != nil {
		t.Errorf("second RemovePiHooks() error: %v", err)
	}
}

func TestRemovePiHooksRefusesForeignFile(t *testing.T) {
	dir := t.TempDir()
	extDir := filepath.Join(dir, "extensions")
	if err := os.MkdirAll(extDir, 0755); err != nil {
		t.Fatal(err)
	}
	path := PiExtensionPath(dir)
	if err := os.WriteFile(path, []byte("// someone else's extension\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := RemovePiHooks(dir); err == nil {
		t.Fatal("expected RemovePiHooks() to refuse a non-enclaude extension")
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("foreign extension should not be deleted: %v", err)
	}
}
