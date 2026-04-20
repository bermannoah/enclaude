package store

import (
	"strings"
	"testing"
)

func TestFormatSize(t *testing.T) {
	tests := []struct {
		bytes    int64
		expected string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1024 * 1024, "1.0 MB"},
		{1572864, "1.5 MB"},
		{1024 * 1024 * 1024, "1.0 GB"},
	}
	for _, tc := range tests {
		got := FormatSize(tc.bytes)
		if got != tc.expected {
			t.Errorf("FormatSize(%d) = %q, want %q", tc.bytes, got, tc.expected)
		}
	}
}

func TestSealStatsHasChanges(t *testing.T) {
	if !(SealStats{Added: 1}).HasChanges() {
		t.Error("Added > 0 should HasChanges")
	}
	if !(SealStats{Modified: 1}).HasChanges() {
		t.Error("Modified > 0 should HasChanges")
	}
	if !(SealStats{Deleted: 1}).HasChanges() {
		t.Error("Deleted > 0 should HasChanges")
	}
	if (SealStats{Unchanged: 5}).HasChanges() {
		t.Error("only Unchanged should not HasChanges")
	}
}

func TestSealStatsStringIncludesDeletedWhenNonZero(t *testing.T) {
	s := SealStats{Scanned: 10, Added: 1, Modified: 2, Deleted: 1, Unchanged: 6}
	if !strings.Contains(s.String(), "deleted") {
		t.Errorf("expected 'deleted' when Deleted > 0, got %q", s.String())
	}
}

func TestSealStatsStringOmitsDeletedWhenZero(t *testing.T) {
	s := SealStats{Scanned: 10, Added: 1, Modified: 2, Unchanged: 7}
	if strings.Contains(s.String(), "deleted") {
		t.Errorf("expected no 'deleted' when Deleted == 0, got %q", s.String())
	}
}

func TestUnsealStatsStringIncludesDeletedWhenNonZero(t *testing.T) {
	s := UnsealStats{Total: 5, Restored: 3, Unchanged: 1, Deleted: 1}
	if !strings.Contains(s.String(), "deleted") {
		t.Errorf("expected 'deleted' when Deleted > 0, got %q", s.String())
	}
}

func TestUnsealStatsStringOmitsDeletedWhenZero(t *testing.T) {
	s := UnsealStats{Total: 5, Restored: 3, Unchanged: 2}
	if strings.Contains(s.String(), "deleted") {
		t.Errorf("expected no 'deleted' when Deleted == 0, got %q", s.String())
	}
}
