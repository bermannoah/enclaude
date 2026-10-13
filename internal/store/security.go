package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// localStateFile returns a path for local, non-synced state (pending sensitive
// restores and the managed-path ledger). It lives under .git so it is never
// committed; the worktree fallback is only used when .git is unavailable and is
// covered by .gitignore.
func localStateFile(sealDir, name string) string {
	gitDir := filepath.Join(sealDir, ".git")
	if info, err := os.Stat(gitDir); err == nil && info.IsDir() {
		return filepath.Join(gitDir, name)
	}
	return filepath.Join(sealDir, ".enclaude-"+name)
}

func loadPathSet(path string) map[string]bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]bool{}
	}
	var paths []string
	if err := json.Unmarshal(data, &paths); err != nil {
		return map[string]bool{}
	}
	out := make(map[string]bool, len(paths))
	for _, p := range paths {
		out[p] = true
	}
	return out
}

func savePathSet(path string, set map[string]bool) error {
	if len(set) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	paths := make([]string, 0, len(set))
	for p := range set {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	data, err := json.MarshalIndent(paths, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// pendingFile holds paths whose remote sensitive version was skipped during an
// unseal, so a later seal does not treat them as local deletions.
func pendingFile(sealDir string) string { return localStateFile(sealDir, "pending.json") }

func loadPending(sealDir string) map[string]bool { return loadPathSet(pendingFile(sealDir)) }

func savePending(sealDir string, pending map[string]bool) error {
	return savePathSet(pendingFile(sealDir), pending)
}

// managedFile is the local ledger of paths this device has sealed. Deletions
// are decided against this ledger rather than the synced include patterns, so a
// compromised remote cannot cause unseal to delete files that were never sealed.
func managedFile(sealDir string) string { return localStateFile(sealDir, "managed.json") }

func loadManaged(sealDir string) map[string]bool { return loadPathSet(managedFile(sealDir)) }

// saveManaged always writes the ledger, even when empty, so its existence marks
// that it has been initialized and must not be re-seeded from the manifest.
func saveManaged(sealDir string, managed map[string]bool) error {
	paths := make([]string, 0, len(managed))
	for p := range managed {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	data, err := json.MarshalIndent(paths, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(managedFile(sealDir), data, 0600)
}

// instructionPaths are the auto-applied instruction files, used to label a
// skipped resource with a human-readable reason.
var instructionPaths = []string{
	"AGENTS.md",
	"AGENTS.override.md",
	"AGENTS.MD",
	"CLAUDE.md",
	"CLAUDE.MD",
	"SYSTEM.md",
	"APPEND_SYSTEM.md",
	"RTK.md",
}

// MatchesSensitive reports whether relPath matches any confirmation pattern.
func MatchesSensitive(relPath string, patterns []string) bool {
	for _, p := range patterns {
		if MatchGlob(relPath, p) {
			return true
		}
	}
	return false
}

// SensitiveReason describes why a resource requires confirmation on unseal.
func SensitiveReason(relPath string) string {
	if MatchesSensitive(relPath, instructionPaths) {
		return "steers the agent"
	}
	switch relPath {
	case "models.json":
		return "changes request routing"
	case "auth.json", "oauth.json", "mcp-auth.json", "trust.json":
		return "credentials/trust"
	default:
		return "runs code"
	}
}

// WithAllowSensitive permits unseal to write sensitive resources (code,
// instructions, routing, credentials). Default false so unattended unseal
// cannot apply content delivered by a remote.
func WithAllowSensitive(allow bool) UnsealOption {
	return func(c *unsealConfig) { c.allowSensitive = allow }
}

// trimEmptyParents removes now-empty parent directories up to (but not
// including) root. Best effort.
func trimEmptyParents(absPath, root string) {
	dir := filepath.Dir(absPath)
	for dir != root && strings.HasPrefix(dir, root) {
		if err := os.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}
