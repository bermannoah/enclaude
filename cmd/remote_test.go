package cmd

import (
	"strings"
	"testing"

	"github.com/coredipper/enclaude/internal/config"
)

func TestMergeDriverCommandPinsSourceAndSealDir(t *testing.T) {
	sealDir := t.TempDir()
	cfg := config.DefaultConfigForSource("pi", "/tmp/pi", sealDir)
	if err := cfg.Save(sealDir); err != nil {
		t.Fatal(err)
	}

	got := mergeDriverCommand(sealDir)

	for _, want := range []string{"--source", "'pi'", "--seal-dir", sealDir, "merge-driver manifest %O %A %B"} {
		if !strings.Contains(got, want) {
			t.Errorf("mergeDriverCommand() = %q, missing %q", got, want)
		}
	}
}
