package secrets

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/traefik-console/internal/config/filesystem"
	"github.com/traefik-console/internal/models"
)

// CaptureRuntime reads the current file of every named secret (nil means the
// file does not exist) as encrypted preimages.
func (m *Vault) CaptureRuntime(ctx context.Context, names []string) (map[string][]byte, error) {
	result := map[string][]byte{}
	for _, name := range names {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		path, e := filesystem.SafeResolvePath(m.secretsDir, name+".secret")
		if e != nil {
			return nil, e
		}
		b, e := filesystem.ReadBounded(path, 1024*1024)
		if os.IsNotExist(e) {
			result[name] = nil
			continue
		}
		if e != nil {
			return nil, e
		}
		cipher, e := m.encrypt(string(b))
		if e != nil {
			return nil, e
		}
		result[name] = cipher
	}
	return result, nil
}

// CandidateRuntime maps the secrets an operation will leave on disk to their ciphertext.
func (m *Vault) CandidateRuntime(rows []models.SecretRow) map[string][]byte {
	result := map[string][]byte{}
	for _, r := range rows {
		if len(r.EncryptedValue) > 0 {
			result[r.Name] = r.EncryptedValue
		}
	}
	return result
}

// RestoreRuntime refuses to overwrite external modifications. Both maps carry
// only encrypted bytes; nil means the file did not exist before the operation.
func (m *Vault) RestoreRuntime(ctx context.Context, prior, attempt map[string][]byte) error {
	for name, cipher := range prior {
		if e := ctx.Err(); e != nil {
			return e
		}
		path, e := filesystem.SafeResolvePath(m.secretsDir, name+".secret")
		if e != nil {
			return e
		}
		current, e := filesystem.ReadBounded(path, 1024*1024)
		if errors.Is(e, os.ErrNotExist) {
			current = nil
		} else if e != nil {
			return e
		}
		var old, next []byte
		if cipher != nil {
			p, e := m.decrypt(cipher)
			if e != nil {
				return e
			}
			old = []byte(p)
		}
		if attempt[name] != nil {
			p, e := m.decrypt(attempt[name])
			if e != nil {
				return e
			}
			next = []byte(p)
		}
		if !bytes.Equal(current, old) && !bytes.Equal(current, next) {
			return fmt.Errorf("secret runtime conflict")
		}
	}
	for name, cipher := range prior {
		path, e := filesystem.SafeResolvePath(m.secretsDir, name+".secret")
		if e != nil {
			return e
		}
		if cipher == nil {
			if e = filesystem.RemoveRegular(path); e != nil {
				return e
			}
			continue
		}
		p, e := m.decrypt(cipher)
		if e != nil {
			return e
		}
		if e = filesystem.WriteFileAtomic(path, []byte(p), 0600); e != nil {
			return e
		}
	}
	return nil
}
