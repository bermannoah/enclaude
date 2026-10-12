package cmd

import (
	"strings"
	"testing"
)

func TestBuildReadme(t *testing.T) {
	tests := []struct {
		name      string
		publicKey string
		deviceID  string
		contains  []string
		absent    []string
	}{
		{
			name:      "contains public key and device ID",
			publicKey: "age1testpublickey",
			deviceID:  "myhost-deadbeef",
			contains: []string{
				"age1testpublickey",
				"myhost-deadbeef",
			},
		},
		{
			name:      "no unresolved placeholders",
			publicKey: "age1abc",
			deviceID:  "host-1234",
			absent: []string{
				"{PUBLIC_KEY}",
				"{DEVICE_ID}",
				"{TICK}",
				"{FENCE}",
			},
		},
		{
			name:      "contains required sections",
			publicKey: "age1abc",
			deviceID:  "host-1234",
			contains: []string{
				"## Restoring on a new machine",
				"## Key recovery",
				"## Daily use",
				"enclaude unseal",
				"enclaude seal",
				"Private keys are never stored here",
			},
		},
		{
			name:      "backticks and fences rendered correctly",
			publicKey: "age1abc",
			deviceID:  "host-1234",
			contains: []string{
				"`enclaude unseal`",
				"```",
			},
			absent: []string{
				"{TICK}",
				"{FENCE}",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildReadme(tt.publicKey, tt.deviceID)
			for _, s := range tt.contains {
				if !strings.Contains(got, s) {
					t.Errorf("expected README to contain %q", s)
				}
			}
			for _, s := range tt.absent {
				if strings.Contains(got, s) {
					t.Errorf("expected README NOT to contain %q", s)
				}
			}
		})
	}
}

func TestBuildReadmeForSourcePi(t *testing.T) {
	got := buildReadmeForSource("age1abc", "host-1234", "pi")

	for _, want := range []string{
		"pi.dev",
		"~/.enclaude-pi",
		"--source pi",
		"age1abc",
		"host-1234",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected pi README to contain %q", want)
		}
	}
	for _, absent := range []string{"{SOURCE}", "{SOURCE_NAME}", "{PUBLIC_KEY}", "{DEVICE_ID}"} {
		if strings.Contains(got, absent) {
			t.Errorf("unresolved placeholder %q in README", absent)
		}
	}

	claude := buildReadmeForSource("age1abc", "host-1234", "claude")
	if !strings.Contains(claude, "Claude Code") {
		t.Error("expected claude README to name Claude Code")
	}
	if strings.Contains(claude, "pi.dev") {
		t.Error("claude README should not mention pi.dev")
	}
}
