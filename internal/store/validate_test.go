package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateRelPath(t *testing.T) {
	ok := []string{
		"settings.json",
		"extensions/hello.ts",
		"skills/review/SKILL.md",
		"sessions/--Users-x--/a.jsonl",
		"a/b/c.txt",
	}
	for _, p := range ok {
		if err := ValidateRelPath(p); err != nil {
			t.Errorf("ValidateRelPath(%q) = %v, want nil", p, err)
		}
	}

	bad := []string{
		"",
		"../escape.txt",
		"a/../../b",
		"/etc/passwd",
		"..\\..\\escape.txt", // backslash traversal must be treated as separators
		"a\\..\\..\\b",
		"with\x00nul",
	}
	for _, p := range bad {
		if err := ValidateRelPath(p); err == nil {
			t.Errorf("ValidateRelPath(%q) = nil, want an error", p)
		}
	}
}

func TestScanFilesUsesSlashKeys(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "c.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	files, err := ScanFiles(dir, []string{"a/**"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range files {
		if f.RelPath == "a/b/c.txt" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected key a/b/c.txt, got %+v", files)
	}
}
