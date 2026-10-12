package crypto

import (
	"fmt"
	"os"
	"path/filepath"

	"filippo.io/age"
	"github.com/zalando/go-keyring"
)

const (
	keychainService = "enclaude"
	// legacyKeychainAccount is the single global account used before per-store
	// keys. Existing installs keep their key here; new keys are scoped to a
	// specific seal store.
	legacyKeychainAccount = "age-private-key"
	envKeyVar             = "ENCLAUDE_KEY"
)

// keychainAccount returns the keychain account name for one seal store.
// Scoping by the store's absolute path lets every seal store hold an
// independent key while sharing a single keychain service.
func keychainAccount(sealDir string) string {
	abs, err := filepath.Abs(sealDir)
	if err != nil {
		abs = sealDir
	}
	return legacyKeychainAccount + "@" + filepath.Clean(abs)
}

// StoreKey saves the age private key for a seal store to the OS keychain.
func StoreKey(sealDir string, identity *age.X25519Identity) error {
	return keyring.Set(keychainService, keychainAccount(sealDir), identity.String())
}

// LoadKey retrieves the age private key for a seal store, trying (in order):
//  1. ENCLAUDE_KEY environment variable
//  2. OS keychain, keyed by the seal store
//  3. OS keychain, legacy global entry (installs from before per-store keys)
//
// Returns the identity and the source it was loaded from.
func LoadKey(sealDir string) (*age.X25519Identity, string, error) {
	if envKey := os.Getenv(envKeyVar); envKey != "" {
		id, err := ParseIdentity(envKey)
		if err != nil {
			return nil, "", fmt.Errorf("parsing %s: %w", envKeyVar, err)
		}
		return id, "env", nil
	}

	if secret, err := keyring.Get(keychainService, keychainAccount(sealDir)); err == nil {
		id, err := ParseIdentity(secret)
		if err != nil {
			return nil, "", fmt.Errorf("parsing keychain key: %w", err)
		}
		return id, "keychain", nil
	}

	// Backwards compatibility: stores created before per-store keys shared one
	// global keychain entry.
	if secret, err := keyring.Get(keychainService, legacyKeychainAccount); err == nil {
		id, err := ParseIdentity(secret)
		if err != nil {
			return nil, "", fmt.Errorf("parsing legacy keychain key: %w", err)
		}
		return id, "keychain (legacy)", nil
	}

	return nil, "", fmt.Errorf("no key found for %s (checked %s env var and OS keychain)", sealDir, envKeyVar)
}

// LoadPublicKey loads just the public key (for encryption-only operations like seal).
func LoadPublicKey(sealDir string) (*age.X25519Recipient, string, error) {
	id, source, err := LoadKey(sealDir)
	if err != nil {
		return nil, "", err
	}
	return id.Recipient(), source, nil
}

// DeleteKey removes the age private key for a seal store from the OS keychain.
func DeleteKey(sealDir string) error {
	return keyring.Delete(keychainService, keychainAccount(sealDir))
}
