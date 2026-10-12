package store

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// ValidateRelPath rejects manifest keys that are not safe to join under the
// source directory. Manifest contents come from the (potentially compromised)
// encrypted repository, so an unchecked key such as "../../.ssh/authorized_keys"
// would let a remote writer overwrite arbitrary files on unseal.
func ValidateRelPath(rel string) error {
	if rel == "" {
		return fmt.Errorf("empty path")
	}
	if strings.IndexByte(rel, 0) >= 0 {
		return fmt.Errorf("path contains a NUL byte")
	}
	// Manifest keys are slash-separated, but treat backslashes as separators
	// too so a key that looks local on Unix cannot traverse on Windows.
	norm := strings.ReplaceAll(rel, "\\", "/")
	if !filepath.IsLocal(filepath.FromSlash(norm)) {
		return fmt.Errorf("path is absolute or escapes the source directory")
	}
	return nil
}

// relToOS converts a (slash-separated) manifest key to an OS path fragment for
// joining with the source directory.
func relToOS(rel string) string {
	return filepath.FromSlash(rel)
}

// ValidHash reports whether h is a 64-character hex SHA-256 content hash.
func ValidHash(h string) bool {
	if len(h) != 64 {
		return false
	}
	for i := 0; i < len(h); i++ {
		c := h[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		case c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

// InvalidEntries returns the sorted list of manifest keys whose path or content
// hash is unsafe. An empty result means the manifest is safe to apply.
func (m *Manifest) InvalidEntries() []string {
	if m == nil {
		return nil
	}
	var invalid []string
	for relPath, entry := range m.Files {
		if err := ValidateRelPath(relPath); err != nil {
			invalid = append(invalid, relPath)
			continue
		}
		if !ValidHash(entry.ContentHash) {
			invalid = append(invalid, relPath)
		}
	}
	sort.Strings(invalid)
	return invalid
}
