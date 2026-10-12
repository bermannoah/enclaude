package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coredipper/enclaude/internal/config"
	"github.com/coredipper/enclaude/internal/crypto"
	sealstore "github.com/coredipper/enclaude/internal/store"
)

// TestMergeManifestsPiUnionAndLastWriteWins drives the pi merge driver end to
// end: distinct session files must be unioned, and a conflicting settings.json
// must follow last_write_wins.
func TestMergeManifestsPiUnionAndLastWriteWins(t *testing.T) {
	sealDir := t.TempDir()
	sourceDir := t.TempDir()

	identity, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey() error: %v", err)
	}
	t.Setenv("ENCLAUDE_KEY", identity.String())

	cfg := config.DefaultConfigForSource(config.SourcePi, sourceDir, sealDir)
	if err := cfg.Save(sealDir); err != nil {
		t.Fatal(err)
	}

	// Point the merge driver at this store.
	prev := flagSealDir
	flagSealDir = sealDir
	defer func() { flagSealDir = prev }()

	store := sealstore.NewObjectStore(sealDir)
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}

	put := func(content string) string {
		hash := sealstore.ContentHash([]byte(content))
		enc, err := crypto.Encrypt([]byte(content), identity.Recipient())
		if err != nil {
			t.Fatalf("Encrypt() error: %v", err)
		}
		if err := store.Write(hash, enc); err != nil {
			t.Fatalf("Write() error: %v", err)
		}
		return hash
	}

	dir := t.TempDir()
	writeManifest := func(name string, files map[string]sealstore.FileEntry) string {
		path := filepath.Join(dir, name)
		data, _ := json.Marshal(sealstore.Manifest{
			Version:  2,
			DeviceID: "test",
			SealedAt: time.Now().UTC().Format(time.RFC3339),
			Files:    files,
		})
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}

	baseSettings := `{"defaultProvider":"base"}`
	oursSettings := `{"defaultProvider":"ours"}`
	theirsSettings := `{"defaultProvider":"theirs"}`
	baseHash := put(baseSettings)
	oursHash := put(oursSettings)
	theirsHash := put(theirsSettings)

	sessionAHash := put(`{"type":"session","id":"sess-a"}`)
	sessionBHash := put(`{"type":"session","id":"sess-b"}`)

	baseMtime := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	oursMtime := time.Now().Add(-30 * time.Minute).UTC().Format(time.RFC3339)
	theirsMtime := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)

	ancestor := writeManifest("ancestor.json", map[string]sealstore.FileEntry{
		"settings.json": {ContentHash: baseHash, Mtime: baseMtime},
	})
	ours := writeManifest("ours.json", map[string]sealstore.FileEntry{
		"settings.json":                     {ContentHash: oursHash, Mtime: oursMtime, MergeStrategy: "last_write_wins"},
		"sessions/--proj--/session-a.jsonl": {ContentHash: sessionAHash, Mtime: oursMtime, MergeStrategy: "immutable"},
	})
	theirs := writeManifest("theirs.json", map[string]sealstore.FileEntry{
		"settings.json":                     {ContentHash: theirsHash, Mtime: theirsMtime, MergeStrategy: "last_write_wins"},
		"sessions/--proj--/session-b.jsonl": {ContentHash: sessionBHash, Mtime: theirsMtime, MergeStrategy: "immutable"},
	})

	if err := mergeManifests(ancestor, ours, theirs); err != nil {
		t.Fatalf("mergeManifests() error: %v", err)
	}

	mergedData, err := os.ReadFile(ours)
	if err != nil {
		t.Fatal(err)
	}
	var merged sealstore.Manifest
	if err := json.Unmarshal(mergedData, &merged); err != nil {
		t.Fatalf("parsing merged manifest: %v", err)
	}

	// Union: both distinct sessions survive.
	for _, p := range []string{"sessions/--proj--/session-a.jsonl", "sessions/--proj--/session-b.jsonl"} {
		if _, ok := merged.Files[p]; !ok {
			t.Errorf("merged manifest missing %s", p)
		}
	}
	// last_write_wins: theirs has the later mtime and must win.
	if got := merged.Files["settings.json"].ContentHash; got != theirsHash {
		t.Errorf("settings.json hash = %s, want theirs %s", got, theirsHash)
	}

	// The winning content must be decryptable from the store.
	enc, err := store.Read(theirsHash)
	if err != nil {
		t.Fatalf("reading merged settings object: %v", err)
	}
	plain, err := crypto.Decrypt(enc, identity)
	if err != nil {
		t.Fatalf("decrypting merged settings: %v", err)
	}
	if string(plain) != theirsSettings {
		t.Errorf("merged settings content = %q, want %q", plain, theirsSettings)
	}

	// The driver must also persist the merged manifest into the seal store.
	onDisk, err := sealstore.LoadManifest(sealDir)
	if err != nil {
		t.Fatal(err)
	}
	if onDisk.Files["settings.json"].ContentHash != theirsHash {
		t.Error("seal store manifest was not updated with the merged result")
	}
}

func TestMergeManifestsPiImmutablePrefersOurs(t *testing.T) {
	sealDir := t.TempDir()
	sourceDir := t.TempDir()

	identity, _ := crypto.GenerateKey()
	t.Setenv("ENCLAUDE_KEY", identity.String())

	cfg := config.DefaultConfigForSource(config.SourcePi, sourceDir, sealDir)
	if err := cfg.Save(sealDir); err != nil {
		t.Fatal(err)
	}
	prev := flagSealDir
	flagSealDir = sealDir
	defer func() { flagSealDir = prev }()

	store := sealstore.NewObjectStore(sealDir)
	store.Init()

	hashOf := func(content string) string {
		hash := sealstore.ContentHash([]byte(content))
		enc, _ := crypto.Encrypt([]byte(content), identity.Recipient())
		store.Write(hash, enc)
		return hash
	}
	oursHash := hashOf(`{"type":"session","id":"same","n":1}`)
	theirsHash := hashOf(`{"type":"session","id":"same","n":2}`)

	dir := t.TempDir()
	writeManifest := func(name string, hash string) string {
		path := filepath.Join(dir, name)
		data, _ := json.Marshal(sealstore.Manifest{
			Version:  2,
			SealedAt: time.Now().UTC().Format(time.RFC3339),
			Files: map[string]sealstore.FileEntry{
				"sessions/--proj--/same.jsonl": {ContentHash: hash, MergeStrategy: "immutable"},
			},
		})
		os.WriteFile(path, data, 0600)
		return path
	}
	ancestor := writeManifest("ancestor.json", "")
	ours := writeManifest("ours.json", oursHash)
	theirs := writeManifest("theirs.json", theirsHash)

	if err := mergeManifests(ancestor, ours, theirs); err != nil {
		t.Fatalf("mergeManifests() error: %v", err)
	}

	var merged sealstore.Manifest
	data, _ := os.ReadFile(ours)
	json.Unmarshal(data, &merged)
	if got := merged.Files["sessions/--proj--/same.jsonl"].ContentHash; got != oursHash {
		t.Errorf("immutable merge kept %s, want ours %s", got, oursHash)
	}
}
