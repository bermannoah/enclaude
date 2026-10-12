package cmd

import (
	"testing"

	"github.com/coredipper/enclaude/internal/config"
)

// TestResolveHookTargetInfersPiFromConfig ensures hooks are installed into the
// pi agent dir even when --source is omitted, by reading it from the seal store.
func TestResolveHookTargetInfersPiFromConfig(t *testing.T) {
	sealDir := t.TempDir()
	cfg := config.DefaultConfigForSource(config.SourcePi, "/tmp/pi-agent-dir", sealDir)
	if err := cfg.Save(sealDir); err != nil {
		t.Fatal(err)
	}

	prevSeal, prevSource, prevSourceDir := flagSealDir, flagSource, flagSourceDir
	flagSealDir, flagSource, flagSourceDir = sealDir, "", "/tmp/pi-agent-dir"
	defer func() { flagSealDir, flagSource, flagSourceDir = prevSeal, prevSource, prevSourceDir }()

	source, dir, err := resolveHookTarget()
	if err != nil {
		t.Fatalf("resolveHookTarget() error: %v", err)
	}
	if source != config.SourcePi {
		t.Errorf("source = %q, want %q", source, config.SourcePi)
	}
	if dir != "/tmp/pi-agent-dir" {
		t.Errorf("dir = %q, want /tmp/pi-agent-dir", dir)
	}
}
