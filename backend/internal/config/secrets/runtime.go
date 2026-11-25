package secrets

import (
	"context"
	"fmt"
	"os"

	"github.com/traefik-console/internal/config/filesystem"
	"github.com/traefik-console/internal/models"
)

// FlushSecretsToDisk atomically writes the candidate secrets to 0600 files and
// removes the files of deleted secrets. Files are compared with the COMMITTED
// value first: a file edited outside the console is never overwritten or removed.
func (m *Vault) FlushSecretsToDisk(ctx context.Context, candidate []models.SecretRow, deletes []string) error {
	for _, sec := range candidate {
		plaintext, err := m.decrypt(sec.EncryptedValue)
		if err != nil {
			return fmt.Errorf("%w (%s): %v", ErrDecryptionFailed, sec.Name, err)
		}
		targetPath, err := filesystem.SafeResolvePath(m.secretsDir, sec.Name+".secret")
		if err != nil {
			return err
		}
		current, readErr := filesystem.ReadBounded(targetPath, 1024*1024)
		if readErr != nil && !os.IsNotExist(readErr) {
			return readErr
		}
		if readErr == nil {
			if e := m.checkCommitted(ctx, sec.Name, current); e != nil {
				return e
			}
			if string(current) == plaintext {
				continue
			}
		}
		if err := filesystem.WriteFileAtomic(targetPath, []byte(plaintext), 0600); err != nil {
			return err
		}
	}
	for _, name := range deletes {
		targetPath, err := filesystem.SafeResolvePath(m.secretsDir, name+".secret")
		if err != nil {
			return err
		}
		current, readErr := filesystem.ReadBounded(targetPath, 1024*1024)
		if os.IsNotExist(readErr) {
			continue
		}
		if readErr != nil {
			return readErr
		}
		if e := m.checkCommitted(ctx, name, current); e != nil {
			return e
		}
		if err := filesystem.RemoveRegular(targetPath); err != nil {
			return err
		}
	}
	return nil
}

// checkCommitted requires an existing file to equal the committed secret value.
func (m *Vault) checkCommitted(ctx context.Context, name string, current []byte) error {
	committed, e := m.store.GetSecret(ctx, name)
	if e != nil {
		return e
	}
	if committed == nil || len(committed.EncryptedValue) == 0 {
		return ErrRuntimeConflict
	}
	active, e := m.decrypt(committed.EncryptedValue)
	if e != nil {
		return e
	}
	if string(current) != active {
		return ErrRuntimeConflict
	}
	return nil
}
