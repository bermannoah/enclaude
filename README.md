# enclaude

Encrypted, git-backed, cross-device sync for `~/.claude/` and `~/.pi/agent/`.

enclaude treats each supported agent as a **source profile**: it seals the
source directory into an age-encrypted git repository and restores it on any
device. Claude Code and [pi.dev](https://pi.dev) are supported out of the box;
see [pi.dev support](#pidev-support).

## The Problem

Claude Code stores everything in plaintext at `~/.claude/`:

- **`history.jsonl`** — every prompt you've ever typed, timestamped
- **Session JSONL files** — full conversation transcripts including tool calls, tool results, and any file content Claude read during the session
- **Memory files** — project-specific context Claude remembers between sessions
- **Settings and stats** — your configuration, usage patterns, plugin list

This means your `~/.claude/` directory contains a detailed record of your work: code snippets, error messages, file paths, environment variables, and anything else that appeared in a session. It sits on disk as readable text with no encryption, no signatures, and no tamper detection.

This isn't a bug — it's how every AI coding assistant works today (Cursor, Copilot, Windsurf all store history in plaintext too). But it means:

1. **Anyone with access to your disk can read your full Claude history.** If your laptop is lost, stolen, or accessed by another user, all session data is exposed.
2. **There's no way to sync sessions across devices.** Your history on your work laptop and your personal machine are completely separate.
3. **There's no version history.** If a session file is corrupted or a memory file is overwritten, there's no way to recover a previous state.

`enclaude` addresses all three.

## What It Does

`enclaude` sits between Claude Code and your filesystem. It doesn't modify Claude Code — it works alongside it using a two-directory architecture:

```
~/.claude/              plaintext (what Claude Code reads/writes)
     |
     |  seal (encrypt)
     v
~/.enclaude/         encrypted git repo
  manifest.json         file index: path -> SHA-256 hash, merge strategy
  seal.toml             config: include/exclude patterns, device ID
  key.age.backup        passphrase-encrypted key backup
  objects/              content-addressed age-encrypted blobs
     |
     |  git push/pull
     v
  remote repo           synced across devices
```

**Seal** encrypts changed files from `~/.claude/` into content-addressed objects using [age](https://age-encryption.org/) (ChaCha20-Poly1305 + X25519). Each file is hashed (SHA-256) and encrypted individually. Only changed files are re-encrypted — unchanged files are skipped by comparing hashes.

**Unseal** decrypts the objects back to `~/.claude/` so Claude Code can use them.

**Git** provides the transport layer. The encrypted objects are committed to a git repository, giving you full version history, branching, and remote sync — all on encrypted data. Your plaintext never leaves your machine; only encrypted blobs are pushed.

### Why This Works Well for Claude Data

Claude Code's session files have a property that makes sync trivial: **they're immutable after completion**. Once a session ends, its JSONL file is never modified again. This means:

- Two devices that both ran sessions produce different files with different hashes — no conflicts, just union both sides
- `history.jsonl` is append-only — merging two diverged copies means deduplicating lines and sorting by timestamp
- Memory files are small markdown — standard 3-way text merge handles them

The only files that need real merge logic are `settings.json` (last-write-wins) and `history.jsonl` (line-level dedup). Everything else is either immutable or trivially mergeable.

## Quick Start

```bash
# Install
go install github.com/coredipper/enclaude@latest

# Initialize — generates an age key, stores it in your OS keychain,
# encrypts all ~/.claude/ data into ~/.enclaude/
enclaude init

# See what's changed since last seal
enclaude status

# Encrypt changes
enclaude seal

# Decrypt back to ~/.claude/
enclaude unseal
```

### Set Up Cross-Device Sync

```bash
# Create a private repo for your encrypted data
# (only encrypted blobs are pushed — your plaintext never leaves your machine)
enclaude remote add origin git@github.com:you/enclaude-data.git
enclaude push

# On another device — clone the encrypted repo and import your key
git clone git@github.com:you/enclaude-data.git ~/.enclaude
enclaude key import --from-backup   # or: enclaude key import keyfile.txt
enclaude unseal --yes               # --yes applies settings/extensions on a deliberate restore
enclaude hooks install
```

### Auto-Sync with Hooks

```bash
enclaude hooks install
```

This adds `SessionStart` and `SessionEnd` hooks to `~/.claude/settings.json`. When a session starts, `enclaude` unseals the latest sealed data. When it ends, it seals changes locally. To enable automatic remote sync, set `auto_push = true` and `auto_pull = true` in `~/.enclaude/seal.toml`. Your existing hooks (peon-ping, notchi, etc.) are preserved — the installer appends to the hooks array, never overwrites. For pi.dev, install a managed extension instead with `enclaude hooks install --source pi`; see [pi.dev support](#pidev-support). Because the managed hook records the local binary, seal-store, and source paths, re-running `enclaude hooks install` refreshes them — do this after [restoring a store on a new device](#new-device-onboarding).

## pi.dev Support

pi.dev stores its state in `~/.pi/agent/` (configurable with
`PI_CODING_AGENT_DIR`). enclaude ships a `pi` source profile that understands
that layout and defaults to a separate seal store so you can seal both agents
side by side.

| | Claude Code | pi.dev |
|---|---|---|
| Source flag | `--source claude` (default) | `--source pi` |
| Source directory | `~/.claude` | `~/.pi/agent` (or `$PI_CODING_AGENT_DIR`) |
| Default seal store | `~/.enclaude` | `~/.enclaude-pi` |
| Hooks | `SessionStart`/`SessionEnd` in `settings.json` | managed extension at `extensions/enclaude.ts` |

```bash
# Initialize a pi seal store
export ENCLAUDE_SOURCE=pi          # or pass --source pi to every command
enclaude init --source pi

enclaude status --source pi
enclaude seal   --source pi
enclaude unseal --source pi
```

Setting `ENCLAUDE_SOURCE=pi` (or aliasing `enclaude` to `enclaude --source pi`)
means you rarely pass the flag explicitly.

### What gets sealed

| Included | Why |
|---|---|
| `settings.json`, `models.json`, `models-store.json`, `keybindings.json`, `mcp.json` | User configuration |
| `AGENTS.md`, `SYSTEM.md`, `APPEND_SYSTEM.md` and friends | Instructions |
| `sessions/**/*.jsonl` | Conversation history |
| `extensions/**`, `skills/**`, `prompts/**`, `themes/**`, `tools/**` | User resources |

| Excluded | Why |
|---|---|
| `auth.json`, `oauth.json`, `mcp-auth.json` | Device-local credentials (see [Credentials](#credentials)) |
| `trust.json` | Device-local project-trust decisions |
| `bin/**`, `npm/**` | Bundled binaries and installed packages (large, regenerated) |
| `mcp.log*`, `*.log`, `crashes.json`, `diagnostics.json` | Machine-local logs and diagnostics |
| `extensions/enclaude.ts` | The auto-sync extension is device-local |
| `*.swp`, `*.tmp`, `.DS_Store` | Editor and OS noise |

### Merge behaviour

Pi sessions are unique per session and append-only while active, so
`sessions/**/*.jsonl` uses the `immutable` union strategy. Session files are
never rewritten by a merge. Small JSON configuration uses `last_write_wins`,
and Markdown/TypeScript resources merge as text.

### Auto-sync with the pi extension

```bash
enclaude hooks install --source pi
```

This writes `~/.pi/agent/extensions/enclaude.ts`, a small managed extension
that runs `enclaude hook-handler session-start` (pull + unseal) when a pi
session starts and `enclaude hook-handler session-end` (seal + push) when it
shuts down. The extension resolves the `enclaude` binary at runtime
(`$ENCLAUDE_BIN`, `~/.local/bin`, `~/go/bin`, Homebrew paths, then `PATH`), so
the same file works across machines. It is excluded from the seal so an unseal
never deletes a freshly installed hook. Remove it with
`enclaude hooks remove --source pi`.

### Credentials

Credentials are **not** sealed. `~/.pi/agent/auth.json`, `oauth.json`, and
`mcp-auth.json` are excluded because they are device-local secrets, and because
OAuth refresh tokens rotate: restoring a stale copy from another device can
silently sign a device out. The recommended ways to pair credentials with
enclaude are:

- **Environment variables** (best for CI and servers): keep keys such as
  `ANTHROPIC_API_KEY` in your shell/secret manager. Pi picks them up at runtime
  and nothing secret ever lands in the store.
- **Secret-manager references**: pi resolves `auth.json` values prefixed with
  `!` as a command, so you can store `"!op read op://vault/pi/key"` and keep the
  secret in 1Password, `pass`, etc. The command is config; the secret is not.
- **Per-device login**: run `/login` on each device. Credentials stay local and
  you re-authenticate once per machine.

If you keep API keys inline in `models.json` (`apiKey`), note that `models.json`
*is* sealed — prefer `$ENV_VAR` references so the value stays out of the store.
If you really want to sync a credential file, remove it from the `[exclude]`
list in `~/.enclaude-pi/seal.toml` and use a strong key backup passphrase (see
[Security Model](#security-model)).

Credential and trust files (`auth.json`, `oauth.json`, `mcp-auth.json`,
`trust.json`) are also in the confirmation set: even though they are excluded
from a normal seal, a hostile remote can inject them into the manifest, so any
change to them requires `enclaude unseal --yes`.

### Sensitive resources and auto-unseal

Some synced files change what the agent *does*, not just what it remembers. They
fall into three groups:

| Group | Files | Why it is gated |
|---|---|---|
| Code | `settings.json`, `mcp.json`, `extensions/**`, `tools/**`, `skills/**`, `prompts/**`, `commands/**` | Runs with your privileges on the next session |
| Instructions | `SYSTEM.md`, `APPEND_SYSTEM.md`, `AGENTS.md`, `AGENTS.override.md`, `AGENTS.MD`, `CLAUDE.md`, `CLAUDE.MD`, `RTK.md` | Auto-applied every session; the agent has tools, so text is a code-execution and exfiltration primitive |
| Routing | `models.json` | Provider `baseUrl`/`apiKey` decide where prompts and keys are sent |

Anyone who can write to the encrypted remote and knows the public key published
in the seal-store README can push valid ciphertext, so these files are the
behavior-changing surface of a synced store.

`enclaude unseal` therefore **does not write changed sensitive resources by
default**. It lists them (with a short reason) and leaves them in place. Review
the changes, then apply them explicitly:

```bash
enclaude diff            # inspect what changed
enclaude unseal --yes    # apply code/instructions/routing
```

The auto-sync hook (Claude Code or pi) never passes `--yes`, so a remote push
cannot install code that runs on the next session, and cannot rewrite your
system prompt, without your confirmation. A skipped sensitive file stays in the
manifest (tracked as *pending*, in the store's `.git` directory and never
synced) so a later seal does not mistake it for a local deletion. `enclaude pull`
and `enclaude sync` behave the same way and accept `--yes`.

The set is extensible via `[confirm]` in `seal.toml`, but only *additively*: the
built-in patterns always apply, because `seal.toml` is synced and a compromised
remote must not be able to switch the gate off. To apply sensitive changes, run
`enclaude unseal --yes` for that restore.

> This gate covers *unattended* application of behavior-changing files. It does
> not make synced session history safe to resume: a hostile session still feeds
> attacker text to a tool-equipped model. Treat the remote as trusted.

### pi limitations

- **Custom session directories are not followed.** If you set `sessionDir`,
  `PI_CODING_AGENT_SESSION_DIR`, or `--session-dir`, sessions live outside the
  source directory and are not sealed. Keep sessions inside `~/.pi/agent` or
  symlink them in.
- **Project `.pi/` configuration is not sealed.** Project-level settings live
  in each repository and should be versioned with that repository, not in your
  personal encrypted store.
- **Sessions migrated in place.** Pi rewrites old session files when it loads
  and migrates them, so two devices may hold different bytes for the same
  session. The `immutable` strategy keeps the local copy in that edge case.

## Commands

### Core
| Command | Description |
|---------|-------------|
| `init` | Generate age keypair, store in OS keychain, initial seal, write README.md to seal store |
| `seal` | Encrypt changed files, commit to seal store |
| `unseal` | Decrypt seal store to the source directory (add `--yes` to apply changed sensitive resources) |
| `status` | Show changes since last seal |
| `sync` | Seal + pull + push (the daily driver) |
| `push` | Seal + git push |
| `pull` | Git pull + merge + unseal |

### History & Recovery
| Command | Description |
|---------|-------------|
| `log` | Show seal history with commit messages |
| `diff [ref]` | Decrypt and diff between current state and a previous commit |
| `rollback <ref>` | Restore the source directory to a previous commit (creates a safety seal first; add `--yes` to apply sensitive resources) |

### Key Management
| Command | Description |
|---------|-------------|
| `key show` | Display public key and source |
| `key export` | Print private key to stdout (pipe to a password manager) |
| `key import <file>` | Import key from file, stdin (`-`), or `--from-backup` |
| `key rotate` | Generate new key, re-encrypt this store's objects, update keychain |

Each seal store gets its own age key on `init`; the keychain entry is scoped to
the store's directory, so sealing `~/.claude` and `~/.pi/agent` into two stores
does not share or clobber a key. Set `ENCLAUDE_KEY` to override the keychain for
a command (useful in CI); note that the environment variable applies to every
store and takes precedence over the keychain, `init` uses it when set instead of
generating a new key, and `key rotate` refuses to run while it is set (the new
key would be shadowed by the environment variable).

> **Moving a seal store moves the key lookup.** Because the keychain entry is
> keyed by the store's directory, relocating a store (say from `~/.enclaude` to
> an external drive) leaves its key behind under the old path. After moving a
> store, re-store the key with `enclaude key import` (or `enclaude key import
> --from-backup`) so it is saved under the new location. Stores created before
> per-store keys keep working through a fallback to the old global keychain
> entry.

### Maintenance
| Command | Description |
|---------|-------------|
| `repair` | Verify integrity and fix missing objects by re-sealing from plaintext |
| `repair --check` | Verify-only mode (exit code 1 if issues found, useful for CI) |
| `repair --delete-orphans` | Also remove unreferenced object files |
| `hooks install` | Add auto-sync hooks (Claude Code) or the managed pi extension |
| `hooks remove` | Remove auto-sync hooks |
| `hooks status` | Check if hooks are installed |
| `readme-regen` | Regenerate and commit README.md in the seal store |

## Merge Strategies

When pulling from a remote, two devices may have diverged. `enclaude` uses a custom git merge driver that applies different strategies depending on the file type:

| Strategy | Used for | How it works |
|----------|----------|-------------|
| `immutable` | Session JSONL files | Union both sides — sessions never change after completion, so there are no conflicts |
| `jsonl_dedup` | `history.jsonl` | Parse each line as JSON, SHA-256 hash for dedup, sort by timestamp |
| `sessions_index` | `sessions-index.json` | Deduplicate entries by `sessionId`, preserving unique sessions from both sides |
| `last_write_wins` | `settings.json`, `stats-cache.json` | Keep whichever version has the later modification time |
| `text_merge` | Memory files (`.md`) | Standard 3-way text merge; conflict markers if both sides changed |

These strategies are configurable per path pattern in `seal.toml`.

## Configuration

`~/.enclaude/seal.toml` controls what gets synced and how:

```toml
[seal]
# "claude" or "pi". Selects the default include/exclude/merge profile.
source = "claude"
# Directory being sealed. Formerly claude_dir (still accepted on load).
source_dir = "~/.claude"
seal_dir = "~/.enclaude"
device_id = "macbook-ab12cd34"

[sync]
auto_seal_on_session_end = true
auto_unseal_on_session_start = true
auto_push = false
auto_pull = false

[include]
patterns = [
  "history.jsonl",
  "settings.json",
  "projects/*/*.jsonl",
  "projects/*/memory/**",
  "projects/*/subagents/**",
]

[exclude]
patterns = [
  "statsig/**",       # feature flag caches — regenerated automatically
  "plugins/**",       # 200+ MB of cached plugin data — regenerated
  "debug/**",         # debug logs
  "hooks/**",         # your hook scripts — version these separately
  "settings.local.json",  # device-specific paths and permissions
]

[merge_strategies]
"history.jsonl" = "jsonl_dedup"
"projects/*/*.jsonl" = "immutable"
"settings.json" = "last_write_wins"
"projects/*/memory/**" = "text_merge"

# Additional paths whose remote changes require `enclaude unseal --yes`. This
# is additive: the built-in set (code, auto-applied instructions, request
# routing) always applies and cannot be removed here, since seal.toml is synced.
[confirm]
patterns = [
  "notes/**",
]
```

The `source_dir` in `seal.toml` is a hint, not an authority. Because `seal.toml`
is synced from the remote, neither the `source` profile nor `source_dir` is
trusted for filesystem operations: enclaude uses the locally selected profile
and its default directory (`~/.claude`, `~/.pi/agent`, or `PI_CODING_AGENT_DIR`)
unless it matches what `seal.toml` records, and otherwise fails closed and asks
for `--source`/`--source-dir`. Passing `--seal-dir` explicitly also confirms the
store, so its recorded profile is used for the default directory (useful for
`enclaude hooks status --seal-dir ~/.enclaude-pi`). A default install therefore
never reads or writes outside its expected directory, even if the remote is
compromised. Custom layouts must pass `--source-dir` on every command (put it in
an alias). `seal_dir` is likewise pinned to the location the config was loaded
from.

For the same reason, unseal only deletes files it has actually sealed before,
tracked in a local, non-synced ledger under the store's `.git` directory. A
compromised remote can therefore not broaden `[include]` to make unseal delete
files that were never part of the seal.

## Security Model

| Property | Status |
|----------|--------|
| Encrypted at rest (between sessions) | Yes — completed session plaintext can be shredded after seal |
| Encrypted at rest (during active session) | No — Claude Code requires plaintext to function |
| Encrypted in transit (git push/pull) | Yes — only age-encrypted blobs are pushed |
| Key storage | OS keychain (macOS Keychain, Linux secret-service, Windows Credential Manager), one entry per seal store |
| Key backup | Passphrase-encrypted `key.age.backup` travels with the repo |
| Tamper detection | SHA-256 content hashes in manifest; `repair --check` verifies integrity |
| Manifest path safety | Unseal validates every path and content hash; entries that are absolute, traverse (`../`), or carry a malformed hash are refused and can be dropped with `repair` |
| Remote trust boundary | The remote is untrusted for paths, source profile/dir, and deletion scope; changed sensitive resources are not auto-applied (see [Sensitive resources and auto-unseal](#sensitive-resources-and-auto-unseal)) |
| Key never in git | Correct — only the passphrase-encrypted backup is committed |
| Credentials | `auth.json`, `oauth.json`, and `mcp-auth.json` are excluded from the seal by default (see [Credentials](#credentials)) |

### Honest Limitations

- **During an active Claude Code session, plaintext exists on disk.** This is unavoidable — Claude Code reads `~/.claude/` directly and cannot be modified to read encrypted data. Use OS-level disk encryption (FileVault, BitLocker, LUKS) for protection during sessions.
- **Application-level encryption does not protect against a malicious process running as your user.** If an attacker has code execution as your user, they can read decrypted files in memory or extract the key from the keychain. OS-level protections are the right defense layer here.
- **The encryption key must be shared across devices.** This is inherent to any cross-device sync scheme. Use `key export` to save your key in a password manager, or rely on the passphrase-encrypted `key.age.backup` that travels with the repo.
- **Keychain entries are tied to the seal store path.** Moving a seal store does not carry its key with it; re-import the key after relocating a store. See [Key Management](#key-management).
- **A synced store is a code-execution boundary.** It carries sensitive resources: code (settings, extensions, tools, skills, prompts), auto-applied instructions (`SYSTEM.md`, `AGENTS.md`, …), and request routing (`models.json`). enclaude refuses to auto-apply changed sensitive resources, but once you run `unseal --yes` a compromised remote can run code, rewrite the system prompt, or redirect model traffic. Treat the remote as trusted, and review changes with `enclaude diff` before applying them. See [Sensitive resources and auto-unseal](#sensitive-resources-and-auto-unseal).

## How It Works Under the Hood

### Content-Addressed Storage

Like git itself, `enclaude` stores objects by their content hash. When you seal a file:

1. Read the plaintext from `~/.claude/`
2. Compute SHA-256 of the plaintext → this becomes the content address
3. Encrypt the plaintext with your age public key
4. Store the encrypted blob at `objects/<hash[0:2]>/<hash[2:]>.age`
5. Record the mapping in `manifest.json`

On the next seal, unchanged files produce the same hash and are skipped entirely. Only new or modified files are encrypted. This makes incremental seals fast — typically under 1 second after a normal session.

Manifest keys are stored with forward slashes and are re-validated on every unseal, so a store written on one operating system restores correctly on another and a crafted key cannot escape the source directory.

### Session Lifecycle

With hooks installed, the flow is:

```
Session starts → hook fires → pull latest + unseal
    ↓
Claude Code runs (reads/writes ~/.claude/ as normal)
    ↓
Session ends → hook fires → seal changes + push
```

The hook handler acquires a file lock (`~/.enclaude/.seal.lock`) to prevent concurrent seal/unseal operations. If the lock can't be acquired within 5 seconds, the hook exits silently — it never blocks Claude Code. Mutating CLI commands (`seal`, `unseal`, `pull`, `push`, `sync`, `repair`, `rollback`, `key rotate`) take the same lock (waiting up to 30 seconds), so a manual command and a session hook can't race on the manifest or the local state files. The lock is released before the outbound `git push`, so a long transfer doesn't stall a session hook.

### New Device Onboarding

```
1. Install enclaude
2. Clone your encrypted data repo
3. Import your key (from password manager, file, or backup passphrase)
4. Pull + unseal (add --yes to restore settings/extensions on this deliberate restore)
5. Install hooks
```

Your full Claude Code history, memory, and settings appear on the new device,
encrypted in transit and at rest. Step 4 needs `--yes` because settings and other
executable resources are not applied automatically; see [Sensitive resources and
auto-unseal](#sensitive-resources-and-auto-unseal).

## License

MIT

