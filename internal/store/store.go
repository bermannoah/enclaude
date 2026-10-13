package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"filippo.io/age"
	"github.com/coredipper/enclaude/internal/config"
	"github.com/coredipper/enclaude/internal/crypto"
)

// SealStats tracks what happened during a seal operation.
type SealStats struct {
	Scanned   int
	Added     int
	Modified  int
	Deleted   int
	Unchanged int
	Errors    int
}

// HasChanges returns true if the seal produced any modifications worth committing.
func (s SealStats) HasChanges() bool {
	return s.Added > 0 || s.Modified > 0 || s.Deleted > 0
}

func (s SealStats) String() string {
	if s.Deleted > 0 {
		return fmt.Sprintf("scanned %d files: %d new, %d modified, %d deleted, %d unchanged",
			s.Scanned, s.Added, s.Modified, s.Deleted, s.Unchanged)
	}
	return fmt.Sprintf("scanned %d files: %d new, %d modified, %d unchanged",
		s.Scanned, s.Added, s.Modified, s.Unchanged)
}

// UnsealStats tracks what happened during an unseal operation.
type UnsealStats struct {
	Total     int
	Restored  int
	Unchanged int
	Deleted   int
	Errors    int
	// SkippedSensitive lists sensitive resources that were not written because
	// the caller did not allow it (UnsealOptions.AllowSensitiveResources).
	SkippedSensitive []string
}

func (s UnsealStats) String() string {
	base := fmt.Sprintf("%d files: %d restored, %d unchanged",
		s.Total, s.Restored, s.Unchanged)
	if s.Deleted > 0 {
		base += fmt.Sprintf(", %d deleted", s.Deleted)
	}
	if len(s.SkippedSensitive) > 0 {
		base += fmt.Sprintf(", %d sensitive resources skipped (need --yes)", len(s.SkippedSensitive))
	}
	return base
}

// UnsealOptions configures an unseal.
type UnsealOptions struct {
	Verbose bool
	// AllowSensitiveResources permits writing sensitive resources: code,
	// auto-applied instructions, and request-routing config (see
	// config.DefaultConfirmPatterns and the [confirm] section of seal.toml).
	// When false, such files are left untouched and reported in
	// UnsealStats.SkippedSensitive. This gates auto-unseal from applying
	// content delivered by a remote until the user explicitly confirms with
	// --yes.
	AllowSensitiveResources bool
	Progress                ProgressFunc
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
	return matchesAny(relPath, patterns)
}

// SensitiveReason describes why a resource requires confirmation on unseal.
// It is a best-effort label for the warning; custom [confirm] patterns default
// to "runs code".
func SensitiveReason(relPath string) string {
	switch {
	case matchesAny(relPath, instructionPaths):
		return "steers the agent"
	case relPath == "models.json":
		return "changes request routing"
	default:
		return "runs code"
	}
}

// localStateFile returns a path for local, non-synced state. It lives under
// .git so it is never committed or synced; the worktree fallback is only used
// when there is no .git directory (and is covered by .gitignore).
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
// that the ledger has been initialized and must not be re-seeded from the
// (possibly remote) manifest.
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

// ProgressFunc is called during long operations to report progress.
type ProgressFunc func(current, total int, path string)

// Seal encrypts changed files from claudeDir into the seal store.
func Seal(cfg *config.Config, recipient age.Recipient, verbose bool, progress ProgressFunc) (SealStats, error) {
	var stats SealStats
	sealDir := cfg.Seal.SealDir

	store := NewObjectStore(sealDir)
	if err := store.Init(); err != nil {
		return stats, fmt.Errorf("initializing object store: %w", err)
	}

	// Load existing manifest (may be nil on first seal)
	manifest, err := LoadManifest(sealDir)
	if err != nil {
		return stats, fmt.Errorf("loading manifest: %w", err)
	}
	if manifest == nil {
		manifest = NewManifest(cfg.Seal.DeviceID)
	}
	manifest.DeviceID = cfg.Seal.DeviceID
	// A brand-new manifest must be persisted even if the source is empty.
	newManifest := len(manifest.Files) == 0

	// Scan files
	files, err := ScanFiles(cfg.Seal.SourceDir, cfg.Include.Patterns, cfg.Exclude.Patterns)
	if err != nil {
		return stats, fmt.Errorf("scanning files: %w", err)
	}
	stats.Scanned = len(files)

	// Track which files still exist (for deletion detection)
	seen := make(map[string]bool)

	for i, f := range files {
		if progress != nil {
			progress(i+1, len(files), f.RelPath)
		}
		seen[f.RelPath] = true

		plaintext, err := os.ReadFile(f.AbsPath)
		if err != nil {
			if verbose {
				fmt.Fprintf(os.Stderr, "  warning: cannot read %s: %v\n", f.RelPath, err)
			}
			stats.Errors++
			continue
		}

		hash := ContentHash(plaintext)

		// Check if unchanged
		if existing, ok := manifest.Files[f.RelPath]; ok && existing.ContentHash == hash {
			stats.Unchanged++
			continue
		}

		// Encrypt and store
		encrypted, err := crypto.Encrypt(plaintext, recipient)
		if err != nil {
			if verbose {
				fmt.Fprintf(os.Stderr, "  warning: cannot encrypt %s: %v\n", f.RelPath, err)
			}
			stats.Errors++
			continue
		}

		if err := store.Write(hash, encrypted); err != nil {
			stats.Errors++
			continue
		}

		// Determine if this is new or modified
		if _, existed := manifest.Files[f.RelPath]; existed {
			stats.Modified++
		} else {
			stats.Added++
		}

		if verbose {
			action := "new"
			if _, existed := manifest.Files[f.RelPath]; existed {
				action = "mod"
			}
			fmt.Printf("  [%s] %s (%s)\n", action, f.RelPath, FormatSize(f.Size))
		}

		// Count JSONL lines if applicable
		lineCount := 0
		if strings.HasSuffix(f.RelPath, ".jsonl") {
			lineCount = bytes.Count(plaintext, []byte("\n"))
			if len(plaintext) > 0 && plaintext[len(plaintext)-1] != '\n' {
				lineCount++ // last line without trailing newline
			}
		}

		manifest.Files[f.RelPath] = FileEntry{
			ContentHash:     hash,
			SizePlaintext:   f.Size,
			SizeEncrypted:   int64(len(encrypted)),
			Mtime:           time.UnixMilli(f.ModTimeMs).UTC().Format(time.RFC3339),
			MergeStrategy:   ResolveMergeStrategy(f.RelPath, cfg.Merge),
			Mode:            f.Mode,
			JSONLLineCount:  lineCount,
			SessionComplete: isSessionComplete(f.RelPath),
		}
	}

	// Decide deletions against the local managed-path ledger, not the synced
	// include patterns. A manifest entry is dropped only if this device has
	// sealed it before and it is genuinely gone from disk; entries this device
	// never had (remote-only, pending, or merely excluded by a narrow include)
	// are kept. This stops a compromised seal.toml from causing data loss.
	pending := loadPending(sealDir)
	managed := loadManaged(sealDir)
	if _, err := os.Stat(managedFile(sealDir)); os.IsNotExist(err) {
		// First run against an existing store: seed from the manifest so
		// pre-upgrade local deletions still propagate, then maintain locally.
		for path := range manifest.Files {
			managed[path] = true
		}
	}
	for path := range manifest.Files {
		if seen[path] || pending[path] || !managed[path] {
			continue
		}
		if ValidateRelPath(path) != nil {
			continue // never joined with the source dir; repair drops these
		}
		if _, err := os.Stat(filepath.Join(cfg.Seal.SourceDir, relToOS(path))); err == nil {
			continue // still on disk (e.g. a narrow include no longer scans it)
		}
		if verbose {
			fmt.Printf("  [del] %s\n", path)
		}
		delete(manifest.Files, path)
		stats.Deleted++
	}

	// Everything scanned on disk is managed by this device.
	for path := range seen {
		managed[path] = true
	}
	if err := saveManaged(sealDir, managed); err != nil {
		return stats, fmt.Errorf("saving managed ledger: %w", err)
	}

	// Pending paths that now exist locally (sealed, unchanged, or manually
	// recreated) or were removed from the manifest are no longer pending.
	if len(pending) > 0 {
		for path := range pending {
			if seen[path] {
				delete(pending, path)
				continue
			}
			if _, inManifest := manifest.Files[path]; !inManifest {
				delete(pending, path)
			}
		}
		if err := savePending(sealDir, pending); err != nil {
			return stats, fmt.Errorf("saving pending state: %w", err)
		}
	}

	// Save the manifest only when something actually changed. Saving on a no-op
	// seal would bump SealedAt and leave the seal store dirty, which then makes
	// a subsequent `git pull` refuse to merge over the modified manifest.json.
	if stats.HasChanges() || newManifest {
		if err := manifest.Save(sealDir); err != nil {
			return stats, fmt.Errorf("saving manifest: %w", err)
		}
	}

	return stats, nil
}

// Unseal decrypts seal contents back to the source directory and removes
// managed files not in the manifest. The manifest is the source of truth.
//
// Sensitive resources (code, auto-applied instructions, and request-routing
// config; see config.DefaultConfirmPatterns) are only written when
// opts.AllowSensitiveResources is set; otherwise they are reported in
// UnsealStats.SkippedSensitive and recorded as pending so a later seal does not
// mistake them for deletions.
func Unseal(cfg *config.Config, identity age.Identity, opts UnsealOptions) (UnsealStats, error) {
	var stats UnsealStats
	sealDir := cfg.Seal.SealDir

	store := NewObjectStore(sealDir)

	manifest, err := LoadManifest(sealDir)
	if err != nil {
		return stats, fmt.Errorf("loading manifest: %w", err)
	}
	if manifest == nil {
		return stats, fmt.Errorf("no manifest found — is the seal store initialized?")
	}

	// Refuse to write anything if the manifest contains unsafe entries. The
	// manifest is read from the (possibly compromised) repository, so an entry
	// such as "../../.ssh/authorized_keys" must never be joined with SourceDir.
	if invalid := manifest.InvalidEntries(); len(invalid) > 0 {
		return stats, fmt.Errorf("refusing to unseal: manifest contains %d unsafe entr%s: %s (run 'enclaude repair' to drop them)",
			len(invalid), plural(len(invalid), "y", "ies"), strings.Join(invalid, ", "))
	}

	stats.Total = len(manifest.Files)
	pending := loadPending(sealDir)

	// Confirmation patterns are the built-in safe set unioned with any patterns
	// from seal.toml. The synced config can only add confirmations, never remove
	// them, so a compromised remote cannot disable this gate.
	confirmPatterns := config.EffectiveConfirmPatterns(cfg.Confirm.Patterns)

	i := 0
	for relPath, entry := range manifest.Files {
		i++
		if opts.Progress != nil {
			opts.Progress(i, stats.Total, relPath)
		}
		absPath := filepath.Join(cfg.Seal.SourceDir, relToOS(relPath))

		// Check if file already exists and matches
		if existing, err := os.ReadFile(absPath); err == nil {
			if ContentHash(existing) == entry.ContentHash {
				stats.Unchanged++
				delete(pending, relPath)
				continue
			}
		}

		// Gate sensitive resources unless the caller explicitly allowed them.
		if !opts.AllowSensitiveResources && MatchesSensitive(relPath, confirmPatterns) {
			stats.SkippedSensitive = append(stats.SkippedSensitive, relPath)
			pending[relPath] = true
			continue
		}

		// Read encrypted object
		encrypted, err := store.Read(entry.ContentHash)
		if err != nil {
			if opts.Verbose {
				fmt.Fprintf(os.Stderr, "  warning: missing object for %s: %v\n", relPath, err)
			}
			stats.Errors++
			continue
		}

		// Decrypt
		plaintext, err := crypto.Decrypt(encrypted, identity)
		if err != nil {
			if opts.Verbose {
				fmt.Fprintf(os.Stderr, "  warning: cannot decrypt %s: %v\n", relPath, err)
			}
			stats.Errors++
			continue
		}

		// Write to the source directory, then apply the recorded permission bits
		// (WriteFile only honors the mode when creating a new file).
		dir := filepath.Dir(absPath)
		if err := os.MkdirAll(dir, 0700); err != nil {
			stats.Errors++
			continue
		}
		mode := os.FileMode(entry.Mode) & 0o777
		if mode == 0 {
			mode = 0600
		}
		if err := os.WriteFile(absPath, plaintext, mode); err != nil {
			stats.Errors++
			continue
		}
		if err := os.Chmod(absPath, mode); err != nil {
			stats.Errors++
			continue
		}

		delete(pending, relPath)
		if opts.Verbose {
			fmt.Printf("  [restore] %s (%s)\n", relPath, FormatSize(entry.SizePlaintext))
		}
		stats.Restored++
	}

	// Persist which executable resources are still pending confirmation.
	if err := savePending(sealDir, pending); err != nil {
		return stats, fmt.Errorf("saving pending state: %w", err)
	}

	// Delete files this device previously managed that are no longer in the
	// manifest (remote deletion). Deletions are driven by the local managed
	// ledger, never by the synced include patterns, so a compromised remote can
	// only delete files that were actually sealed — not arbitrary files that
	// happen to match a broadened include. Skip if restore had errors.
	if stats.Errors > 0 {
		return stats, nil
	}
	managed := loadManaged(sealDir)
	for path := range managed {
		if _, inManifest := manifest.Files[path]; inManifest {
			continue
		}
		if ValidateRelPath(path) != nil {
			continue
		}
		absPath := filepath.Join(cfg.Seal.SourceDir, relToOS(path))
		if err := os.Remove(absPath); err == nil {
			stats.Deleted++
			if opts.Verbose {
				fmt.Printf("  [delete] %s\n", path)
			}
			dir := filepath.Dir(absPath)
			if dir != cfg.Seal.SourceDir {
				os.Remove(dir)
			}
		}
	}

	return stats, nil
}

// plural picks singular or plural suffix for count.
func plural(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}

// Status returns the diff between the current claude directory and the seal manifest.
func Status(cfg *config.Config) (*DiffResult, error) {
	manifest, err := LoadManifest(cfg.Seal.SealDir)
	if err != nil {
		return nil, fmt.Errorf("loading manifest: %w", err)
	}

	files, err := ScanFiles(cfg.Seal.SourceDir, cfg.Include.Patterns, cfg.Exclude.Patterns)
	if err != nil {
		return nil, fmt.Errorf("scanning files: %w", err)
	}

	// Build a "current" manifest from disk
	current := NewManifest(cfg.Seal.DeviceID)
	for _, f := range files {
		data, err := os.ReadFile(f.AbsPath)
		if err != nil {
			continue
		}
		current.Files[f.RelPath] = FileEntry{
			ContentHash: ContentHash(data),
		}
	}

	diff := current.Diff(manifest)

	// The "deleted" bucket must mirror what Seal actually drops: an entry is
	// deleted only if this device previously managed it, it is genuinely gone
	// from disk, and it is not pending (a skipped sensitive restore).
	diff.Deleted = nil
	managed := loadManaged(cfg.Seal.SealDir)
	if _, err := os.Stat(managedFile(cfg.Seal.SealDir)); os.IsNotExist(err) {
		for path := range manifest.Files {
			managed[path] = true
		}
	}
	pending := loadPending(cfg.Seal.SealDir)
	for path := range manifest.Files {
		if _, onDisk := current.Files[path]; onDisk {
			continue
		}
		if pending[path] || !managed[path] {
			continue
		}
		if ValidateRelPath(path) != nil {
			continue
		}
		if _, err := os.Stat(filepath.Join(cfg.Seal.SourceDir, relToOS(path))); err == nil {
			continue // still on disk (e.g. excluded by a narrow include)
		}
		diff.Deleted = append(diff.Deleted, path)
	}
	sort.Strings(diff.Deleted)

	return &diff, nil
}

// UnsealStatus returns what Unseal would do without actually writing anything.
// "Added" = files in manifest but missing on disk (would be restored).
// "Modified" = files on disk with different content than manifest (would be overwritten).
// "Deleted" = managed files on disk but not in manifest (would be deleted).
func UnsealStatus(cfg *config.Config) (*DiffResult, error) {
	manifest, err := LoadManifest(cfg.Seal.SealDir)
	if err != nil {
		return nil, fmt.Errorf("loading manifest: %w", err)
	}
	if manifest == nil {
		return nil, fmt.Errorf("no manifest found — is the seal store initialized?")
	}

	// Scan existing files. If claudeDir doesn't exist yet (first-time
	// restore), treat as empty — all manifest files would be restored.
	var files []ScanResult
	if _, statErr := os.Stat(cfg.Seal.SourceDir); statErr == nil {
		files, err = ScanFiles(cfg.Seal.SourceDir, cfg.Include.Patterns, cfg.Exclude.Patterns)
		if err != nil {
			return nil, fmt.Errorf("scanning files: %w", err)
		}
	}

	// Build current state from disk
	onDisk := make(map[string]string) // relPath -> hash
	for _, f := range files {
		data, err := os.ReadFile(f.AbsPath)
		if err != nil {
			continue
		}
		onDisk[f.RelPath] = ContentHash(data)
	}

	var result DiffResult

	// Files in manifest: would be restored or overwritten
	for relPath, entry := range manifest.Files {
		diskHash, exists := onDisk[relPath]
		if !exists {
			result.Added = append(result.Added, relPath) // missing on disk, would restore
		} else if diskHash != entry.ContentHash {
			result.Modified = append(result.Modified, relPath) // different, would overwrite
		}
	}

	// Files this device previously managed that are missing from the manifest
	// would be deleted. This mirrors Unseal's ledger-based deletion, not the
	// include-scan, so a broadened or narrowed include cannot fabricate
	// deletions in the preview.
	managed := loadManaged(cfg.Seal.SealDir)
	for relPath := range managed {
		if _, inManifest := manifest.Files[relPath]; inManifest {
			continue
		}
		if ValidateRelPath(relPath) != nil {
			continue
		}
		if _, err := os.Stat(filepath.Join(cfg.Seal.SourceDir, relToOS(relPath))); err == nil {
			result.Deleted = append(result.Deleted, relPath)
		}
	}
	sort.Strings(result.Deleted)

	return &result, nil
}

// ResolveMergeStrategy finds the merge strategy for a file based on glob patterns.
func ResolveMergeStrategy(relPath string, strategies map[string]string) string {
	strategy, _ := ResolveMergeStrategyWithPattern(relPath, strategies)
	return strategy
}

// ResolveMergeStrategyWithPattern returns both the strategy and the pattern that matched.
// An empty pattern means a built-in default was used.
// When multiple glob patterns match, the most specific wins (most segments,
// fewest wildcards). This ensures deterministic resolution regardless of
// Go map iteration order.
func ResolveMergeStrategyWithPattern(relPath string, strategies map[string]string) (strategy string, pattern string) {
	// Try exact match first (highest priority)
	if s, ok := strategies[relPath]; ok {
		return s, relPath
	}

	// Collect all matching glob patterns, pick the most specific
	bestPattern := ""
	bestStrategy := ""
	bestScore := ""

	for p, s := range strategies {
		if p == relPath {
			continue // already checked exact match
		}
		if MatchGlob(relPath, p) {
			score := patternSpecificity(p)
			if score > bestScore {
				bestScore = score
				bestPattern = p
				bestStrategy = s
			}
		}
	}

	if bestPattern != "" {
		return bestStrategy, bestPattern
	}

	// Default
	if strings.HasSuffix(relPath, ".md") {
		return "text_merge", ""
	}
	return "last_write_wins", ""
}

// patternSpecificity returns a comparable score slice for a glob pattern.
// Compared lexicographically, more specific patterns sort higher.
// Per-segment scoring: literal=3, constrained glob (contains [)=2, *=1, **=0.
// Ties broken by segment count (more = more specific) then pattern string.
func patternSpecificity(pattern string) string {
	segs := strings.Split(pattern, "/")
	// Sum per-segment scores: literal=3, glob=2, *=1, **=0.
	// Higher total = more specific.
	total := 0
	for _, seg := range segs {
		switch {
		case seg == "**":
			total += 0
		case seg == "*":
			total += 1
		case strings.ContainsAny(seg, "*?["):
			total += 2
		default:
			total += 3
		}
	}
	// Fixed-width: total score (2 digits), segment count (2 digits), pattern.
	// Higher total wins; ties broken by more segments, then lexical.
	return fmt.Sprintf("%02d:%02d:%s", total, len(segs), pattern)
}

// isSessionComplete determines if a session JSONL file is likely complete.
// Claude Code sessions live under projects/ and pi sessions live under
// sessions/. Both are treated as complete once they exist; active sessions
// keep appending, which only produces newer hashes on the next seal.
func isSessionComplete(relPath string) bool {
	if !strings.HasSuffix(relPath, ".jsonl") {
		return false
	}
	return strings.HasPrefix(relPath, "projects/") || strings.HasPrefix(relPath, "sessions/")
}

// RepairResult describes the outcome of a seal store integrity check.
type RepairResult struct {
	TotalManifest  int
	TotalOnDisk    int
	MissingObjects []string // manifest entries with no .age file
	CorruptObjects []string // objects that fail decrypt or hash mismatch
	OrphanObjects  []string // .age files not in manifest
	// InvalidEntries lists manifest keys with unsafe paths or hashes. They are
	// never written on unseal and are dropped by Repair.
	InvalidEntries []string
	Fixed          int
}

// Verify checks seal store integrity without modifying anything.
func Verify(cfg *config.Config, identity age.Identity, verbose bool) (*RepairResult, error) {
	sealDir := cfg.Seal.SealDir
	store := NewObjectStore(sealDir)
	result := &RepairResult{}

	manifest, err := LoadManifest(sealDir)
	if err != nil {
		return nil, fmt.Errorf("loading manifest: %w", err)
	}
	if manifest == nil {
		return nil, fmt.Errorf("no manifest found")
	}

	result.TotalManifest = len(manifest.Files)
	result.InvalidEntries = manifest.InvalidEntries()

	// Check all manifest entries have objects
	for path, entry := range manifest.Files {
		// Unsafe entries are reported separately and never read.
		if ValidateRelPath(path) != nil || !ValidHash(entry.ContentHash) {
			continue
		}
		if !store.Exists(entry.ContentHash) {
			result.MissingObjects = append(result.MissingObjects, path)
			if verbose {
				fmt.Fprintf(os.Stderr, "  [missing] %s (hash: %s)\n", path, entry.ContentHash[:16])
			}
			continue
		}

		// Verify decrypt + hash
		encrypted, err := store.Read(entry.ContentHash)
		if err != nil {
			result.CorruptObjects = append(result.CorruptObjects, path)
			continue
		}
		plaintext, err := crypto.Decrypt(encrypted, identity)
		if err != nil {
			result.CorruptObjects = append(result.CorruptObjects, path)
			if verbose {
				fmt.Fprintf(os.Stderr, "  [corrupt] %s: decrypt failed\n", path)
			}
			continue
		}
		if ContentHash(plaintext) != entry.ContentHash {
			result.CorruptObjects = append(result.CorruptObjects, path)
			if verbose {
				fmt.Fprintf(os.Stderr, "  [corrupt] %s: hash mismatch\n", path)
			}
		}
	}

	// Find orphan objects
	allOnDisk, err := store.ListAll()
	if err != nil {
		return result, fmt.Errorf("listing objects: %w", err)
	}
	result.TotalOnDisk = len(allOnDisk)

	referenced := make(map[string]bool)
	for _, entry := range manifest.Files {
		referenced[entry.ContentHash] = true
	}
	for _, hash := range allOnDisk {
		if !referenced[hash] {
			result.OrphanObjects = append(result.OrphanObjects, hash)
			if verbose {
				fmt.Fprintf(os.Stderr, "  [orphan] %s\n", hash[:16])
			}
		}
	}

	return result, nil
}

// Repair fixes seal store integrity issues.
func Repair(cfg *config.Config, identity age.Identity, deleteOrphans bool, verbose bool) (*RepairResult, error) {
	result, err := Verify(cfg, identity, verbose)
	if err != nil {
		return nil, err
	}

	manifest, err := LoadManifest(cfg.Seal.SealDir)
	if err != nil {
		return nil, fmt.Errorf("loading manifest: %w", err)
	}

	store := NewObjectStore(cfg.Seal.SealDir)

	// Drop manifest entries with unsafe paths or hashes. They can never be
	// applied and would otherwise block unseal until manually edited out.
	if len(result.InvalidEntries) > 0 {
		pending := loadPending(cfg.Seal.SealDir)
		for _, path := range result.InvalidEntries {
			delete(manifest.Files, path)
			delete(pending, path)
			if verbose {
				fmt.Printf("  [dropped] %s (unsafe manifest entry)\n", path)
			}
		}
		if err := savePending(cfg.Seal.SealDir, pending); err != nil {
			return result, fmt.Errorf("saving pending state: %w", err)
		}
	}

	// Track old hashes that get superseded during repair so we can
	// include them in orphan deletion (they become orphaned only after
	// the manifest is updated with new hashes).
	var superseded []string

	// Try to fix missing/corrupt by re-sealing from plaintext
	for _, path := range append(result.MissingObjects, result.CorruptObjects...) {
		absPath := filepath.Join(cfg.Seal.SourceDir, relToOS(path))
		plaintext, err := os.ReadFile(absPath)
		if err != nil {
			continue // plaintext not available
		}

		hash := ContentHash(plaintext)
		encrypted, err := crypto.Encrypt(plaintext, identity.(*age.X25519Identity).Recipient())
		if err != nil {
			continue
		}
		if err := store.Write(hash, encrypted); err != nil {
			continue
		}

		// Track superseded hash before updating manifest
		oldHash := manifest.Files[path].ContentHash
		if oldHash != hash && oldHash != "" {
			superseded = append(superseded, oldHash)
		}

		// Rebuild full manifest entry from repaired state
		entry := manifest.Files[path]
		entry.ContentHash = hash
		entry.SizePlaintext = int64(len(plaintext))
		entry.SizeEncrypted = int64(len(encrypted))
		entry.MergeStrategy = ResolveMergeStrategy(path, cfg.Merge)
		// Update Mtime and mode from current file stat (important for
		// last_write_wins and for keeping executable bits on repaired scripts).
		if info, err := os.Stat(absPath); err == nil {
			entry.Mtime = info.ModTime().UTC().Format(time.RFC3339)
			entry.Mode = uint32(info.Mode().Perm())
		}
		// Recompute JSONL line count
		if strings.HasSuffix(path, ".jsonl") {
			entry.JSONLLineCount = bytes.Count(plaintext, []byte("\n"))
			if len(plaintext) > 0 && plaintext[len(plaintext)-1] != '\n' {
				entry.JSONLLineCount++
			}
		}
		entry.SessionComplete = isSessionComplete(path)
		manifest.Files[path] = entry

		result.Fixed++
		if verbose {
			fmt.Printf("  [fixed] %s (re-sealed from plaintext)\n", path)
		}
	}

	// Save updated manifest before deleting any objects — if save fails,
	// the old manifest still has valid references to existing objects.
	if result.Fixed > 0 || len(result.InvalidEntries) > 0 {
		if err := manifest.Save(cfg.Seal.SealDir); err != nil {
			return result, fmt.Errorf("saving manifest: %w", err)
		}
	}

	// Delete orphans only after manifest is safely persisted
	if deleteOrphans {
		// Build set of hashes still referenced by the updated manifest
		// so we don't delete objects that another entry still needs
		// (e.g., two files with identical content share one object).
		referenced := make(map[string]bool)
		for _, entry := range manifest.Files {
			referenced[entry.ContentHash] = true
		}

		// Include superseded hashes (old objects replaced during repair)
		allOrphans := append(result.OrphanObjects, superseded...)
		for _, hash := range allOrphans {
			if referenced[hash] {
				continue // still needed by another manifest entry
			}
			store.Delete(hash)
			if verbose {
				fmt.Printf("  [deleted] orphan %s\n", hash[:16])
			}
		}
	}

	return result, nil
}

// Rotate re-encrypts all sealed objects with a new key.
func Rotate(cfg *config.Config, oldIdentity age.Identity, newRecipient age.Recipient, verbose bool, progress ProgressFunc) (int, error) {
	sealDir := cfg.Seal.SealDir
	store := NewObjectStore(sealDir)

	manifest, err := LoadManifest(sealDir)
	if err != nil {
		return 0, fmt.Errorf("loading manifest: %w", err)
	}
	if manifest == nil {
		return 0, fmt.Errorf("no manifest found")
	}

	total := len(manifest.Files)
	rotated := 0
	i := 0

	for path, entry := range manifest.Files {
		i++
		if progress != nil {
			progress(i, total, path)
		}

		encrypted, err := store.Read(entry.ContentHash)
		if err != nil {
			if verbose {
				fmt.Fprintf(os.Stderr, "  warning: cannot read %s: %v\n", path, err)
			}
			continue
		}

		plaintext, err := crypto.Decrypt(encrypted, oldIdentity)
		if err != nil {
			if verbose {
				fmt.Fprintf(os.Stderr, "  warning: cannot decrypt %s: %v\n", path, err)
			}
			continue
		}

		newEncrypted, err := crypto.Encrypt(plaintext, newRecipient)
		if err != nil {
			if verbose {
				fmt.Fprintf(os.Stderr, "  warning: cannot re-encrypt %s: %v\n", path, err)
			}
			continue
		}

		// Overwrite in place — content hash stays the same
		if err := store.Write(entry.ContentHash, newEncrypted); err != nil {
			continue
		}

		rotated++
	}

	// Save manifest (updates SealedAt timestamp)
	if err := manifest.Save(sealDir); err != nil {
		return rotated, fmt.Errorf("saving manifest: %w", err)
	}

	return rotated, nil
}

// FormatSize formats a byte count as a human-readable string.
func FormatSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
