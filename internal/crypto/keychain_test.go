package crypto

import (
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestKeychainScopedPerStore(t *testing.T) {
	keyring.MockInit()
	t.Setenv(envKeyVar, "")

	keyA, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	keyB, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	if err := StoreKey("/tmp/store-a", keyA); err != nil {
		t.Fatalf("StoreKey(a) error: %v", err)
	}
	if err := StoreKey("/tmp/store-b", keyB); err != nil {
		t.Fatalf("StoreKey(b) error: %v", err)
	}

	gotA, _, err := LoadKey("/tmp/store-a")
	if err != nil {
		t.Fatalf("LoadKey(a) error: %v", err)
	}
	gotB, _, err := LoadKey("/tmp/store-b")
	if err != nil {
		t.Fatalf("LoadKey(b) error: %v", err)
	}
	if gotA.Recipient().String() != keyA.Recipient().String() {
		t.Error("store-a did not get its own key")
	}
	if gotB.Recipient().String() != keyB.Recipient().String() {
		t.Error("store-b did not get its own key")
	}
}

func TestKeychainLegacyFallback(t *testing.T) {
	keyring.MockInit()
	t.Setenv(envKeyVar, "")

	legacy, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	// Simulate an install from before per-store keys: single global entry.
	if err := keyring.Set(keychainService, legacyKeychainAccount, legacy.String()); err != nil {
		t.Fatalf("seeding legacy key: %v", err)
	}

	got, source, err := LoadKey("/tmp/brand-new-store")
	if err != nil {
		t.Fatalf("LoadKey() error: %v", err)
	}
	if got.Recipient().String() != legacy.Recipient().String() {
		t.Error("legacy key was not used as a fallback")
	}
	if !strings.Contains(source, "legacy") {
		t.Errorf("source = %q, want it to mention legacy", source)
	}

	// A store-specific key must shadow the legacy entry.
	own, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := StoreKey("/tmp/brand-new-store", own); err != nil {
		t.Fatal(err)
	}
	got, _, err = LoadKey("/tmp/brand-new-store")
	if err != nil {
		t.Fatal(err)
	}
	if got.Recipient().String() != own.Recipient().String() {
		t.Error("store-specific key did not shadow the legacy key")
	}
}
