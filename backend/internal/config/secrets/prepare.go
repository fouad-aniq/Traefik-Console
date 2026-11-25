package secrets

import (
	"context"
	"path/filepath"

	"github.com/traefik-console/internal/config/filesystem"
	"github.com/traefik-console/internal/models"
)

// PrepareSecret is pure with respect to persistence/files: it only encrypts. The
// engine commits the returned row, with the rest of its operation, after publication.
func (m *Vault) PrepareSecret(ctx context.Context, name, purpose, env, value string) (*models.SecretRow, error) {
	if value == "" {
		return nil, ErrSecretEmptyValue
	}
	path, e := filesystem.SafeResolvePath(m.secretsDir, name+".secret")
	if e != nil {
		return nil, e
	}
	if m.runtimeDir != "" {
		path = filepath.Join(m.runtimeDir, name+".secret")
	}
	cipher, e := m.encrypt(value)
	if e != nil {
		return nil, e
	}
	return &models.SecretRow{Name: name, Purpose: purpose, EnvVarName: env, FilePath: path, MaskedPreview: "••••••••", EncryptedValue: cipher}, nil
}

// DecryptSecret decrypts a committed secret by name.
func (m *Vault) DecryptSecret(ctx context.Context, name string) (string, error) {
	sec, err := m.store.GetSecret(ctx, name)
	if err != nil {
		return "", err
	}
	if sec == nil {
		return "", ErrSecretNotFound
	}
	return m.decrypt(sec.EncryptedValue)
}

// Decrypt decrypts ciphertext the engine holds in a candidate that is not committed yet.
func (m *Vault) Decrypt(ciphertext []byte) (string, error) { return m.decrypt(ciphertext) }
