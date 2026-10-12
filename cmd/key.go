package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"filippo.io/age"
	"github.com/coredipper/enclaude/internal/config"
	"github.com/coredipper/enclaude/internal/crypto"
	"github.com/coredipper/enclaude/internal/gitops"
	"github.com/coredipper/enclaude/internal/store"
	"github.com/coredipper/enclaude/internal/ui"
	"github.com/spf13/cobra"
)

var keyCmd = &cobra.Command{
	Use:   "key",
	Short: "Key management commands",
}

var keyShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show the seal store's public key",
	RunE: func(cmd *cobra.Command, args []string) error {
		recipient, source, err := crypto.LoadPublicKey()
		if err != nil {
			return fmt.Errorf("loading key: %w", err)
		}
		fmt.Printf("Public key: %s\n", recipient.String())
		fmt.Printf("Source: %s\n", source)
		return nil
	},
}

var keyExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export the private key (for backup or new device)",
	RunE: func(cmd *cobra.Command, args []string) error {
		identity, source, err := crypto.LoadKey()
		if err != nil {
			return fmt.Errorf("loading key: %w", err)
		}
		fmt.Printf("# enclaude private key (loaded from %s)\n", source)
		fmt.Printf("# public key: %s\n", identity.Recipient().String())
		fmt.Println(identity.String())
		return nil
	},
}

var importFromBackup bool

var keyImportCmd = &cobra.Command{
	Use:   "import [file | -]",
	Short: "Import a private key from file, stdin, or backup",
	Long: `Import an age private key into the OS keychain.

  enclaude key import keyfile.txt    # from file
  echo "AGE-SECRET-KEY-..." | enclaude key import -  # from stdin
  enclaude key import --from-backup  # decrypt key.age.backup`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var keyData string

		if importFromBackup {
			backupPath := filepath.Join(getSealDir(), "key.age.backup")
			encrypted, err := os.ReadFile(backupPath)
			if err != nil {
				return fmt.Errorf("reading key backup: %w", err)
			}
			fmt.Print("Backup passphrase: ")
			reader := bufio.NewReader(os.Stdin)
			passphrase, _ := reader.ReadString('\n')
			passphrase = strings.TrimSpace(passphrase)

			decrypted, err := crypto.DecryptWithPassphrase(encrypted, passphrase)
			if err != nil {
				return fmt.Errorf("decrypting backup (wrong passphrase?): %w", err)
			}
			keyData = string(decrypted)
		} else if len(args) == 0 {
			return fmt.Errorf("specify a file, '-' for stdin, or --from-backup")
		} else if args[0] == "-" {
			scanner := bufio.NewScanner(os.Stdin)
			var lines []string
			for scanner.Scan() {
				lines = append(lines, scanner.Text())
			}
			keyData = strings.Join(lines, "\n")
		} else {
			data, err := os.ReadFile(args[0])
			if err != nil {
				return fmt.Errorf("reading key file: %w", err)
			}
			keyData = string(data)
		}

		identity, err := crypto.ParseIdentity(strings.TrimSpace(keyData))
		if err != nil {
			return fmt.Errorf("invalid key: %w", err)
		}

		if err := crypto.StoreKey(identity); err != nil {
			return fmt.Errorf("storing key: %w", err)
		}

		fmt.Printf("%s Key imported.\n", ui.Green("OK"))
		fmt.Printf("Public key: %s\n", identity.Recipient().String())
		return nil
	},
}

var keyRotateCmd = &cobra.Command{
	Use:   "rotate",
	Short: "Generate a new key and re-encrypt all sealed objects",
	RunE: func(cmd *cobra.Command, args []string) error {
		activeSealDir := getSealDir()

		// Every seal store on the device shares one keychain entry, so we must
		// re-encrypt all of them before replacing that entry.
		sealDirs := append([]string{activeSealDir}, otherExistingSealDirs(activeSealDir, defaultSealDirCandidates())...)
		if len(sealDirs) > 1 {
			fmt.Printf("Found %d seal stores sharing this key; all will be re-encrypted.\n", len(sealDirs))
		}

		oldIdentity, _, err := crypto.LoadKey()
		if err != nil {
			return fmt.Errorf("loading current key: %w", err)
		}
		oldPub := oldIdentity.Recipient().String()

		fmt.Println("Generating new key...")
		newIdentity, err := crypto.GenerateKey()
		if err != nil {
			return fmt.Errorf("generating new key: %w", err)
		}
		newPub := newIdentity.Recipient().String()

		fmt.Print("\nNew backup passphrase (or Enter to skip): ")
		reader := bufio.NewReader(os.Stdin)
		passphrase, _ := reader.ReadString('\n')
		passphrase = strings.TrimSpace(passphrase)

		fmt.Println("Re-encrypting all objects...")
		if err := rotateKeyAcrossStores(sealDirs, oldIdentity, newIdentity, passphrase, flagVerbose); err != nil {
			return err
		}

		fmt.Printf("\n%s Key rotated.\n", ui.Green("Done."))
		fmt.Printf("  Old public key: %s\n", ui.Faint(oldPub))
		fmt.Printf("  New public key: %s\n", ui.Bold(newPub))
		return nil
	},
}

// rotateKeyAcrossStores re-encrypts every seal store with newIdentity, then
// replaces the shared keychain entry, optionally writes a new passphrase backup
// into each store, and commits each store.
//
// A pre-flight pass verifies that every store is fully readable with the old key
// before anything is modified, and the keychain entry is replaced only after
// every store has been successfully re-encrypted. This prevents a store that
// uses a different key (or has corrupt/missing objects) from stranding the
// others. Rotation is still not transactional against mid-write failures such
// as a full disk; use the passphrase backup to recover in that case.
func rotateKeyAcrossStores(sealDirs []string, oldIdentity, newIdentity *age.X25519Identity, backupPassphrase string, verbose bool) error {
	cfgs := make([]*config.Config, len(sealDirs))
	for i, sealDir := range sealDirs {
		cfg, err := config.Load(sealDir)
		if err != nil {
			return fmt.Errorf("loading config for %s: %w", sealDir, err)
		}
		cfgs[i] = cfg
	}

	// Pre-flight: every store must be fully readable with the current key before
	// we modify anything.
	for i, sealDir := range sealDirs {
		result, err := store.Verify(cfgs[i], oldIdentity, false)
		if err != nil {
			return fmt.Errorf("verifying %s: %w", sealDir, err)
		}
		if len(result.MissingObjects) > 0 || len(result.CorruptObjects) > 0 {
			return fmt.Errorf("refusing to rotate: store %s is not fully readable with the current key (%d missing, %d corrupt objects)",
				sealDir, len(result.MissingObjects), len(result.CorruptObjects))
		}
	}

	for i, sealDir := range sealDirs {
		rotated, err := store.Rotate(cfgs[i], oldIdentity, newIdentity.Recipient(), verbose, nil)
		if err != nil {
			return fmt.Errorf("rotation of %s: %w", sealDir, err)
		}
		fmt.Printf("  %s: re-encrypted %d objects.\n", sealDir, rotated)
	}

	// Replace the shared keychain entry only after every store was re-encrypted.
	if err := crypto.StoreKey(newIdentity); err != nil {
		return fmt.Errorf("storing new key: %w", err)
	}

	// Write the new passphrase backup into every store with a single passphrase.
	if backupPassphrase != "" {
		backup, err := crypto.EncryptWithPassphrase([]byte(newIdentity.String()), backupPassphrase)
		if err != nil {
			return fmt.Errorf("creating backup: %w", err)
		}
		for _, sealDir := range sealDirs {
			if err := os.WriteFile(filepath.Join(sealDir, "key.age.backup"), backup, 0600); err != nil {
				return fmt.Errorf("writing backup to %s: %w", sealDir, err)
			}
		}
		fmt.Println("  Key backups updated.")
	}

	for i, sealDir := range sealDirs {
		git := gitops.New(sealDir)
		if err := git.AddAll(); err != nil {
			return fmt.Errorf("git add: %w", err)
		}
		if err := git.Commit(fmt.Sprintf("seal: key rotation from %s", cfgs[i].Seal.DeviceID)); err != nil {
			return fmt.Errorf("git commit: %w", err)
		}
	}
	return nil
}

func init() {
	keyImportCmd.Flags().BoolVar(&importFromBackup, "from-backup", false, "decrypt key.age.backup")
	keyCmd.AddCommand(keyShowCmd)
	keyCmd.AddCommand(keyExportCmd)
	keyCmd.AddCommand(keyImportCmd)
	keyCmd.AddCommand(keyRotateCmd)
	rootCmd.AddCommand(keyCmd)
}
