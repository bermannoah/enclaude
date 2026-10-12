package cmd

import (
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

// TestStoresUseIndependentKeys proves that generating/storing a key for one
// seal store does not disturb another store's key.
func TestStoresUseIndependentKeys(t *testing.T) {
	keyring.MockInit()
	t.Setenv("ENCLAUDE_KEY", "") // force the keychain (mock) path

	keyA, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	keyB, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	storeA := filepath.Join(t.TempDir(), "sealA")
	storeB := filepath.Join(t.TempDir(), "sealB")

	if err := crypto.StoreKey(storeA, keyA); err != nil {
		t.Fatalf("StoreKey(A) error: %v", err)
	}
	if err := crypto.StoreKey(storeB, keyB); err != nil {
		t.Fatalf("StoreKey(B) error: %v", err)
	}

	gotA, _, err := crypto.LoadKey(storeA)
	if err != nil {
		t.Fatalf("LoadKey(A) error: %v", err)
	}
	gotB, _, err := crypto.LoadKey(storeB)
	if err != nil {
		t.Fatalf("LoadKey(B) error: %v", err)
	}
	if gotA.Recipient().String() != keyA.Recipient().String() {
		t.Error("store A did not retain its own key")
	}
	if gotB.Recipient().String() != keyB.Recipient().String() {
		t.Error("store B did not get its own key")
	}
}

// TestRotateSealStore covers rotating one store's key: the store is
// re-encrypted, the keychain entry is replaced, a new backup is written, and
// the store is committed.
func TestRotateSealStore(t *testing.T) {
	keyring.MockInit()
	t.Setenv("ENCLAUDE_KEY", "")
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

	root := t.TempDir()
	sealDir := filepath.Join(root, "seal")
	content := `{"device":"A"}`
	newSealStore(t, sealDir, filepath.Join(root, "pi"), oldIdentity.Recipient(), content)
	if err := crypto.StoreKey(sealDir, oldIdentity); err != nil {
		t.Fatal(err)
	}

	if err := rotateSealStore(sealDir, oldIdentity, newIdentity, "correct horse", false); err != nil {
		t.Fatalf("rotateSealStore() error: %v", err)
	}

	// The store's keychain entry must hold the new key.
	loaded, _, err := crypto.LoadKey(sealDir)
	if err != nil {
		t.Fatalf("LoadKey() error: %v", err)
	}
	if loaded.Recipient().String() != newIdentity.Recipient().String() {
		t.Error("keychain does not hold the new key after rotation")
	}

	manifest, err := sealstore.LoadManifest(sealDir)
	if err != nil {
		t.Fatal(err)
	}
	hash := manifest.Files["settings.json"].ContentHash

	if plain, err := decryptWithKey(sealDir, hash, newIdentity); err != nil {
		t.Errorf("new key cannot decrypt after rotation: %v", err)
	} else if string(plain) != content {
		t.Error("content mismatch after rotation")
	}
	if _, err := decryptWithKey(sealDir, hash, oldIdentity); err == nil {
		t.Error("old key still decrypts after rotation")
	}

	backup, err := os.ReadFile(filepath.Join(sealDir, "key.age.backup"))
	if err != nil {
		t.Fatalf("missing key backup: %v", err)
	}
	plain, err := crypto.DecryptWithPassphrase(backup, "correct horse")
	if err != nil {
		t.Fatalf("backup will not decrypt: %v", err)
	}
	if strings.TrimSpace(string(plain)) != newIdentity.String() {
		t.Error("backup does not contain the new key")
	}

	out, err := exec.Command("git", "-C", sealDir, "log", "--oneline").Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "key rotation") {
		t.Errorf("no rotation commit:\n%s", out)
	}
}

// TestRotateSealStoreAbortsOnUnreadableStore verifies the pre-flight: a store
// that cannot be read with the current key aborts rotation before the keychain
// is replaced.
func TestRotateSealStoreAbortsOnUnreadableStore(t *testing.T) {
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

	root := t.TempDir()
	sealDir := filepath.Join(root, "seal")
	newSealStore(t, sealDir, filepath.Join(root, "pi"), oldIdentity.Recipient(), `{"good":true}`)
	if err := crypto.StoreKey(sealDir, oldIdentity); err != nil {
		t.Fatal(err)
	}

	// Corrupt every object so Verify fails.
	manifest, err := sealstore.LoadManifest(sealDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range manifest.Files {
		objPath := filepath.Join(sealDir, "objects", entry.ContentHash[:2], entry.ContentHash[2:]+".age")
		if err := os.WriteFile(objPath, []byte("not ciphertext"), 0600); err != nil {
			t.Fatal(err)
		}
	}

	if err := rotateSealStore(sealDir, oldIdentity, newIdentity, "", false); err == nil {
		t.Fatal("expected rotation to abort on the unreadable store")
	}

	// The keychain must still hold the old key.
	loaded, _, err := crypto.LoadKey(sealDir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Recipient().String() != oldIdentity.Recipient().String() {
		t.Error("keychain was replaced despite the abort")
	}
}

// TestInitGeneratesIndependentKeyPerStore runs the real init flow twice and
// confirms each store gets its own freshly generated key, never reusing a
// previously stored one.
func TestInitGeneratesIndependentKeyPerStore(t *testing.T) {
	keyring.MockInit()
	t.Setenv("ENCLAUDE_KEY", "")
	t.Setenv("GIT_AUTHOR_NAME", "test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")

	root := t.TempDir()
	initStore := func(name string) (sealDir string) {
		sealDir = filepath.Join(root, name)
		sourceDir := filepath.Join(root, name+"-source")
		if err := os.MkdirAll(sourceDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sourceDir, "settings.json"), []byte("{}"), 0644); err != nil {
			t.Fatal(err)
		}

		// init prompts for a backup passphrase on stdin; feed it a newline.
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.WriteString("\n"); err != nil {
			t.Fatal(err)
		}
		w.Close()
		prevStdin, prevStdout := os.Stdin, os.Stdout
		devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		os.Stdin, os.Stdout = r, devNull
		defer func() {
			os.Stdin, os.Stdout = prevStdin, prevStdout
			r.Close()
			devNull.Close()
		}()

		prevSource, prevSourceDir, prevSealDir := flagSource, flagSourceDir, flagSealDir
		flagSource, flagSourceDir, flagSealDir = "pi", sourceDir, sealDir
		defer func() { flagSource, flagSourceDir, flagSealDir = prevSource, prevSourceDir, prevSealDir }()

		if err := runInit(initCmd, nil); err != nil {
			t.Fatalf("runInit(%s) error: %v", name, err)
		}
		return sealDir
	}

	storeA := initStore("sealA")
	storeB := initStore("sealB")

	keyA, _, err := crypto.LoadKey(storeA)
	if err != nil {
		t.Fatalf("LoadKey(A) error: %v", err)
	}
	keyB, _, err := crypto.LoadKey(storeB)
	if err != nil {
		t.Fatalf("LoadKey(B) error: %v", err)
	}
	if keyA.Recipient().String() == keyB.Recipient().String() {
		t.Error("second init reused the first store's key")
	}
}
