package cmd

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/coredipper/enclaude/internal/session"
)

// cliLockTimeout is how long a mutating command waits for the seal lock before
// giving up. Session hooks use a much shorter timeout and skip when busy.
const cliLockTimeout = 30 * time.Second

// acquireSealLock takes the seal store lock for a mutating command and returns
// a release function. This serializes CLI runs against each other and against
// the session hooks, which mutate the manifest and the local state files.
func acquireSealLock(sealDir string) (func(), error) {
	lock := session.NewSealLock(sealDir)
	acquired, err := lock.Acquire(cliLockTimeout)
	if err != nil {
		return nil, fmt.Errorf("acquiring seal lock: %w", err)
	}
	if !acquired {
		return nil, fmt.Errorf("another enclaude operation is in progress (%s)", filepath.Join(sealDir, ".seal.lock"))
	}
	return func() { _ = lock.Release() }, nil
}
