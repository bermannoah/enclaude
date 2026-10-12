package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/coredipper/enclaude/internal/config"
	"github.com/coredipper/enclaude/internal/crypto"
	sealstore "github.com/coredipper/enclaude/internal/store"
	"github.com/zalando/go-keyring"
)

// newSealStore creates an initialized git-backed seal store containing a single
// sealed settings.json, encrypted to recipient.
func newSealStore(t *testing.T, sealDir, sourceDir string, recipient age.Recipient, content string) {
	t.Helper()
	if err := os.MkdirAll(sealDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sourceDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "settings.json"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfigForSource(config.SourcePi, sourceDir, sealDir)
	if err := cfg.Save(sealDir); err != nil {
		t.Fatal(err)
	}
	if _, err := sealstore.Seal(cfg, recipient, false, nil); err != nil {
		t.Fatalf("Seal() error: %v", err)
	}

	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"commit", "-qm", "init"}} {
		cmd := exec.Command("git", append([]string{"-C", sealDir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
}

// decryptWithKey returns the plaintext stored at entryHash, or an error.
func decryptWithKey(sealDir, entryHash string, identity age.Identity) ([]byte, error) {
	enc, err := sealstore.NewObjectStore(sealDir).Read(entryHash)
	if err != nil {
		return nil, err
	}
	return crypto.Decrypt(enc, identity)
}

// TestRotateKeyAcrossStores covers the shared-key multi-store rotation path:
// both stores are re-encrypted, the keychain is updated only afterwards, a new
// backup is written to each store, and each store is committed.
func TestRotateKeyAcrossStores(t *testing.T) {
	keyring.MockInit()
	t.Setenv("ENCLAUDE_KEY", "") // force the keychain (mock) path
	t.Setenv("GIT_AUTHOR_NAME", "test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")

	oldIdentity, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	newIdentity, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := crypto.StoreKey(oldIdentity); err != nil {
		t.Fatalf("StoreKey() error: %v", err)
	}

	root := t.TempDir()
	dirs := []string{filepath.Join(root, "sealA"), filepath.Join(root, "sealB")}
	contents := []string{`{"device":"A"}`, `{"device":"B"}`}
	for i, dir := range dirs {
		newSealStore(t, dir, fmt.Sprintf("%s/pi-%d", root, i), oldIdentity.Recipient(), contents[i])
	}

	if err := rotateKeyAcrossStores(dirs, oldIdentity, newIdentity, "correct horse", false); err != nil {
		t.Fatalf("rotateKeyAcrossStores() error: %v", err)
	}

	// The keychain must now hold the new key.
	loaded, _, err := crypto.LoadKey()
	if err != nil {
		t.Fatalf("LoadKey() error: %v", err)
	}
	if loaded.Recipient().String() != newIdentity.Recipient().String() {
		t.Error("keychain does not hold the new key after rotation")
	}

	for i, dir := range dirs {
		manifest, err := sealstore.LoadManifest(dir)
		if err != nil {
			t.Fatalf("store %d: LoadManifest() error: %v", i, err)
		}
		hash := manifest.Files["settings.json"].ContentHash

		// The new key must decrypt every store.
		plain, err := decryptWithKey(dir, hash, newIdentity)
		if err != nil {
			t.Errorf("store %d: new key cannot decrypt: %v", i, err)
		} else if string(plain) != contents[i] {
			t.Errorf("store %d: content mismatch after rotation", i)
		}

		// The old key must no longer decrypt.
		if _, err := decryptWithKey(dir, hash, oldIdentity); err == nil {
			t.Errorf("store %d: old key still decrypts after rotation", i)
		}

		// A fresh passphrase backup must be present and decrypt to the new key.
		backup, err := os.ReadFile(filepath.Join(dir, "key.age.backup"))
		if err != nil {
			t.Errorf("store %d: missing key backup: %v", i, err)
		} else {
			plain, err := crypto.DecryptWithPassphrase(backup, "correct horse")
			if err != nil {
				t.Errorf("store %d: backup will not decrypt: %v", i, err)
			} else if strings.TrimSpace(string(plain)) != newIdentity.String() {
				t.Errorf("store %d: backup does not contain the new key", i)
			}
		}

		// Rotation must be committed in each store.
		out, err := exec.Command("git", "-C", dir, "log", "--oneline").Output()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(out), "key rotation") {
			t.Errorf("store %d: no rotation commit:\n%s", i, out)
		}
	}
}

// TestOtherExistingSealDirs checks discovery of sibling stores while ignoring
// the active one and uninitialized candidates.
func TestOtherExistingSealDirs(t *testing.T) {
	root := t.TempDir()
	active := filepath.Join(root, "active")
	initialized := filepath.Join(root, "initialized")
	empty := filepath.Join(root, "empty")
	for _, dir := range []string{active, initialized, empty} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(initialized, "seal.toml"), []byte("config_version = 3\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(active, "seal.toml"), []byte("config_version = 3\n"), 0600); err != nil {
		t.Fatal(err)
	}

	got := otherExistingSealDirs(active, []string{active, initialized, empty})
	if len(got) != 1 || got[0] != initialized {
		t.Errorf("otherExistingSealDirs() = %v, want [%s]", got, initialized)
	}
}

// TestRotateKeyAcrossStoresAbortsOnUnreadableStore verifies the pre-flight: a
// store that cannot be read with the current key aborts rotation before any
// store is touched and before the keychain is replaced.
func TestRotateKeyAcrossStoresAbortsOnUnreadableStore(t *testing.T) {
	keyring.MockInit()
	t.Setenv("ENCLAUDE_KEY", "")

	oldIdentity, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	newIdentity, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := crypto.StoreKey(oldIdentity); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	good := filepath.Join(root, "sealGood")
	bad := filepath.Join(root, "sealBad")
	newSealStore(t, good, filepath.Join(root, "piGood"), oldIdentity.Recipient(), `{"good":true}`)
	newSealStore(t, bad, filepath.Join(root, "piBad"), oldIdentity.Recipient(), `{"bad":true}`)

	// Corrupt every object in the second store so Verify fails on it.
	manifest, err := sealstore.LoadManifest(bad)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range manifest.Files {
		objPath := filepath.Join(bad, "objects", entry.ContentHash[:2], entry.ContentHash[2:]+".age")
		if err := os.WriteFile(objPath, []byte("not ciphertext"), 0600); err != nil {
			t.Fatal(err)
		}
	}

	err = rotateKeyAcrossStores([]string{good, bad}, oldIdentity, newIdentity, "", false)
	if err == nil {
		t.Fatal("expected rotation to abort on the unreadable store")
	}

	// The keychain must still hold the old key.
	loaded, _, err := crypto.LoadKey()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Recipient().String() != oldIdentity.Recipient().String() {
		t.Error("keychain was replaced despite the abort")
	}

	// The healthy store must still decrypt with the old key.
	goodManifest, err := sealstore.LoadManifest(good)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decryptWithKey(good, goodManifest.Files["settings.json"].ContentHash, oldIdentity); err != nil {
		t.Errorf("healthy store no longer readable with the old key: %v", err)
	}
}
