package cmd

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/coredipper/enclaude/internal/config"
	"github.com/coredipper/enclaude/internal/crypto"
	"github.com/coredipper/enclaude/internal/store"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a new seal store",
	Long:  "Generate an age keypair, store it in the OS keychain, and perform the initial seal.",
	RunE:  runInit,
}

func init() {
	rootCmd.AddCommand(initCmd)
}

func runInit(cmd *cobra.Command, args []string) error {
	source := getSource()
	sourceDir := getSourceDir()
	sealDir := getSealDir()

	// Check source directory exists
	if _, err := os.Stat(sourceDir); os.IsNotExist(err) {
		return fmt.Errorf("%s directory not found at %s", source, sourceDir)
	}

	// Check seal directory doesn't already exist
	if _, err := os.Stat(filepath.Join(sealDir, "seal.toml")); err == nil {
		return fmt.Errorf("seal store already initialized at %s", sealDir)
	}

	fmt.Println("Initializing enclaude...")
	fmt.Printf("  Source:    %s\n", source)
	fmt.Printf("  Source dir: %s\n", sourceDir)
	fmt.Printf("  Seal dir:   %s\n", sealDir)

	// 1. Generate a fresh age keypair for this seal store.
	fmt.Println("\nGenerating age keypair...")
	identity, err := crypto.GenerateKey()
	if err != nil {
		return fmt.Errorf("generating key: %w", err)
	}
	fmt.Printf("  Public key: %s\n", identity.Recipient().String())

	// 2. Store in OS keychain, scoped to this seal store.
	fmt.Println("Storing private key in OS keychain...")
	if err := crypto.StoreKey(sealDir, identity); err != nil {
		return fmt.Errorf("storing key in keychain: %w", err)
	}
	fmt.Println("  Stored in keychain.")

	// 3. Create seal directory
	if err := os.MkdirAll(sealDir, 0700); err != nil {
		return fmt.Errorf("creating seal directory: %w", err)
	}

	// 4. Create passphrase-encrypted backup
	fmt.Print("\nEnter backup passphrase (for key recovery): ")
	reader := bufio.NewReader(os.Stdin)
	passphrase, _ := reader.ReadString('\n')
	passphrase = strings.TrimSpace(passphrase)

	if passphrase != "" {
		backup, err := crypto.EncryptWithPassphrase([]byte(identity.String()), passphrase)
		if err != nil {
			return fmt.Errorf("creating key backup: %w", err)
		}
		backupPath := filepath.Join(sealDir, "key.age.backup")
		if err := os.WriteFile(backupPath, backup, 0600); err != nil {
			return fmt.Errorf("writing key backup: %w", err)
		}
		fmt.Println("  Key backup saved.")
	} else {
		fmt.Println("  Skipping backup (no passphrase entered).")
	}

	// 5. Write default config
	cfg := config.DefaultConfigForSource(source, sourceDir, sealDir)
	if err := cfg.Save(sealDir); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	fmt.Println("  Config written to seal.toml")

	// 6. Initialize git repo
	fmt.Println("\nInitializing git repository...")
	gitInit := exec.Command("git", "init", sealDir)
	gitInit.Stdout = os.Stdout
	gitInit.Stderr = os.Stderr
	if err := gitInit.Run(); err != nil {
		return fmt.Errorf("git init: %w", err)
	}

	// Write .gitattributes for future merge driver
	gitattributes := "manifest.json merge=enclaude-manifest\n"
	if err := os.WriteFile(filepath.Join(sealDir, ".gitattributes"), []byte(gitattributes), 0644); err != nil {
		return fmt.Errorf("writing .gitattributes: %w", err)
	}

	// Write .gitignore
	gitignore := "# Never commit the unencrypted key\n*.key\n"
	if err := os.WriteFile(filepath.Join(sealDir, ".gitignore"), []byte(gitignore), 0644); err != nil {
		return fmt.Errorf("writing .gitignore: %w", err)
	}

	// Write README
	readme := buildReadmeForSource(identity.Recipient().String(), cfg.Seal.DeviceID, source)
	if err := os.WriteFile(filepath.Join(sealDir, "README.md"), []byte(readme), 0644); err != nil {
		return fmt.Errorf("writing README: %w", err)
	}

	// 7. Initial seal
	fmt.Println("\nPerforming initial seal...")
	stats, err := store.Seal(cfg, identity.Recipient(), flagVerbose, nil)
	if err != nil {
		return fmt.Errorf("initial seal: %w", err)
	}
	fmt.Printf("  Sealed: %s\n", stats)

	// 8. Initial commit
	gitAdd := exec.Command("git", "-C", sealDir, "add", ".")
	if err := gitAdd.Run(); err != nil {
		return fmt.Errorf("git add: %w", err)
	}

	gitCommit := exec.Command("git", "-C", sealDir, "commit", "-m",
		fmt.Sprintf("seal: initial seal from %s", cfg.Seal.DeviceID))
	gitCommit.Stdout = os.Stdout
	gitCommit.Stderr = os.Stderr
	if err := gitCommit.Run(); err != nil {
		return fmt.Errorf("git commit: %w", err)
	}

	fmt.Println("\nSeal initialized successfully!")
	fmt.Println("\nNext steps:")
	fmt.Println("  1. enclaude remote add origin <url>   # set up sync remote")
	fmt.Println("  2. enclaude hooks install              # enable auto-sync")
	return nil
}

const readmeTemplate = `# enclaude seal store

This repository contains an encrypted backup of a {SOURCE_NAME} configuration
directory, managed by [enclaude](https://github.com/coredipper/enclaude).

All files are encrypted with [age](https://age-encryption.org/) and cannot be
read without the private key stored in the OS keychain on the originating device.

> **Private keys are never stored here.** The age private key lives exclusively
> in the OS keychain (and optionally in an encrypted backup file). Do not commit
> private keys or unencrypted backups to this repository.

## Repository details

| Field      | Value                    |
|------------|--------------------------|
| Source     | {SOURCE}                 |
| Public key | {PUBLIC_KEY} |
| Device ID  | {DEVICE_ID} |

## Restoring on a new machine

1. Install enclaude.
2. Clone this repository to {TICK}{SEAL_DIR}{TICK}:
   {TICK}git clone <remote-url> {SEAL_DIR}{TICK}
3. Import your private key into the keychain:
   {TICK}enclaude key import{TICK}
4. Decrypt and restore your {SOURCE_NAME} files:
   {TICK}enclaude unseal{TICK}
5. Reinstall auto-sync hooks on this device:
   {TICK}enclaude hooks install{TICK}

{PI_NOTE}

## Key recovery

If the OS keychain entry is lost, restore from the passphrase-encrypted backup:

{FENCE}
enclaude key recover key.age.backup
{FENCE}

You will be prompted for the passphrase you set during {TICK}enclaude init{TICK}.

## Daily use

{FENCE}
enclaude seal          # encrypt and commit changes
enclaude push          # push to remote
enclaude pull          # pull and decrypt latest
enclaude status        # show unsealed changes not yet sealed
{FENCE}
`

func buildReadme(publicKey, deviceID string) string {
	return buildReadmeForSource(publicKey, deviceID, config.SourceClaude)
}

func buildReadmeForSource(publicKey, deviceID, source string) string {
	source = config.NormalizeSource(source)
	sourceName := "Claude Code"
	sealDir := "~/.enclaude"
	piNote := ""
	if source == config.SourcePi {
		sourceName = "pi.dev"
		sealDir = "~/.enclaude-pi"
		piNote = "For pi.dev, " + "`~/.enclaude-pi`" + " is the default seal store. Use\n" +
			"`--source pi` (or `ENCLAUDE_SOURCE=pi`) with every command."
	}
	return strings.NewReplacer(
		"{PUBLIC_KEY}", publicKey,
		"{DEVICE_ID}", deviceID,
		"{SOURCE}", source,
		"{SOURCE_NAME}", sourceName,
		"{SEAL_DIR}", sealDir,
		"{PI_NOTE}", piNote,
		"{TICK}", "`",
		"{FENCE}", "```",
	).Replace(readmeTemplate)
}
