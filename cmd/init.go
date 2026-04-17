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
	claudeDir := getClaudeDir()
	sealDir := getSealDir()

	// Check claude directory exists
	if _, err := os.Stat(claudeDir); os.IsNotExist(err) {
		return fmt.Errorf("claude directory not found at %s", claudeDir)
	}

	// Check seal directory doesn't already exist
	if _, err := os.Stat(filepath.Join(sealDir, "seal.toml")); err == nil {
		return fmt.Errorf("seal store already initialized at %s", sealDir)
	}

	fmt.Println("Initializing enclaude...")
	fmt.Printf("  Claude dir: %s\n", claudeDir)
	fmt.Printf("  Seal dir:  %s\n", sealDir)

	// 1. Generate age keypair
	fmt.Println("\nGenerating age keypair...")
	identity, err := crypto.GenerateKey()
	if err != nil {
		return fmt.Errorf("generating key: %w", err)
	}
	fmt.Printf("  Public key: %s\n", identity.Recipient().String())

	// 2. Store in OS keychain
	fmt.Println("Storing private key in OS keychain...")
	if err := crypto.StoreKey(identity); err != nil {
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
	cfg := config.DefaultConfig(claudeDir, sealDir)
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
	readme := buildReadme(identity.Recipient().String(), cfg.Seal.DeviceID)
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

func buildReadme(publicKey, deviceID string) string {
	tick := "`"
	fence := "```"
	return fmt.Sprintf(
		"# enclaude seal store\n\n"+
			"This repository contains an encrypted backup of a Claude Code configuration\n"+
			"directory, managed by [enclaude](https://github.com/coredipper/enclaude).\n\n"+
			"All files are encrypted with [age](https://age-encryption.org/) and cannot be\n"+
			"read without the private key stored in the OS keychain on the originating device.\n\n"+
			"> **Private keys are never stored here.** The age private key lives exclusively\n"+
			"> in the OS keychain (and optionally in an encrypted backup file). Do not commit\n"+
			"> private keys or unencrypted backups to this repository.\n\n"+
			"## Repository details\n\n"+
			"| Field      | Value                    |\n"+
			"|------------|---------------------------|\n"+
			"| Public key | %s |\n"+
			"| Device ID  | %s |\n\n"+
			"## Restoring on a new machine\n\n"+
			"1. Install enclaude.\n"+
			"2. Clone this repository to %s~/.enclaude/%s:\n"+
			"   %sgit clone <remote-url> ~/.enclaude%s\n"+
			"3. Import your private key into the keychain:\n"+
			"   %senclaude key import%s\n"+
			"4. Decrypt and restore your Claude files:\n"+
			"   %senclaude unseal%s\n\n"+
			"## Key recovery\n\n"+
			"If the OS keychain entry is lost, restore from the passphrase-encrypted backup:\n\n"+
			fence+"\n"+
			"enclaude key recover key.age.backup\n"+
			fence+"\n\n"+
			"You will be prompted for the passphrase you set during %senclaude init%s.\n\n"+
			"## Daily use\n\n"+
			fence+"\n"+
			"enclaude seal          # encrypt and commit changes\n"+
			"enclaude push          # push to remote\n"+
			"enclaude pull          # pull and decrypt latest\n"+
			"enclaude status        # show unsealed changes not yet sealed\n"+
			fence+"\n",
		publicKey, deviceID,
		tick, tick, tick, tick, tick, tick, tick, tick,
		tick, tick,
	)
}
