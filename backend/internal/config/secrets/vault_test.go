package secrets

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/traefik-console/internal/models"
)

type secretRows struct{ rows []models.SecretRow }

func (s *secretRows) ListSecrets(context.Context) ([]models.SecretRow, error) { return s.rows, nil }
func (s *secretRows) GetSecret(_ context.Context, name string) (*models.SecretRow, error) {
	for i := range s.rows {
		if s.rows[i].Name == name {
			return &s.rows[i], nil
		}
	}
	return nil, nil
}

func TestPreparationIsEncryptedNonPersistingAndUsesRuntimeMapping(t *testing.T) {
	root := t.TempDir()

	s := &secretRows{}
	key := make([]byte, 32)
	vault, e := New(root, "/etc/traefik/secrets", key, s)
	if e != nil {
		t.Fatal(e)
	}
	row, e := vault.PrepareSecret(context.Background(), "dns_token", "dns_challenge", "TOKEN_FILE", "synthetic-test-value")
	if e != nil {
		t.Fatal(e)
	}
	if row.FilePath != "/etc/traefik/secrets/dns_token.secret" || len(row.EncryptedValue) == 0 || len(s.rows) != 0 {
		t.Fatal("mapping changed or preparation persisted")
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 0 {
		t.Fatal("preparation wrote runtime files")
	}
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	raw := row.EncryptedValue
	plain, e := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if e != nil || string(plain) != "synthetic-test-value" {
		t.Fatal("legacy nonce-prefixed AES-GCM format changed", e)
	}
}
func TestRuntimeRestorePreservesPreimageAndRefusesExternalChanges(t *testing.T) {
	root := t.TempDir()

	s := &secretRows{}
	v, e := New(root, root, make([]byte, 32), s)
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	first, _ := v.PrepareSecret(ctx, "token", "dns", "", "synthetic-first")
	if e = v.FlushSecretsToDisk(ctx, []models.SecretRow{*first}, nil); e != nil {
		t.Fatal(e)
	}
	// The operation committed: the first value is now the stored secret.
	s.rows = []models.SecretRow{*first}
	before, e := v.CaptureRuntime(ctx, []string{"token"})
	if e != nil {
		t.Fatal(e)
	}
	next, _ := v.PrepareSecret(ctx, "token", "dns", "", "synthetic-next")
	attempt := v.CandidateRuntime([]models.SecretRow{*next})
	if e = v.FlushSecretsToDisk(ctx, []models.SecretRow{*next}, nil); e != nil {
		t.Fatal(e)
	}
	if e = v.RestoreRuntime(ctx, before, attempt); e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(root, "token.secret")
	got, _ := os.ReadFile(p)
	if !bytes.Equal(got, []byte("synthetic-first")) {
		t.Fatal("prior runtime bytes lost")
	}
	if e = os.WriteFile(p, []byte("external-edit"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = v.FlushSecretsToDisk(ctx, []models.SecretRow{*next}, nil); !errors.Is(e, ErrRuntimeConflict) {
		t.Fatal("external write overwritten", e)
	}
	if e = v.FlushSecretsToDisk(ctx, nil, []string{"token"}); !errors.Is(e, ErrRuntimeConflict) {
		t.Fatal("external write removed", e)
	}
	if e = v.RestoreRuntime(ctx, before, attempt); e == nil {
		t.Fatal("external restore overwritten")
	}
}

func TestDeletedSecretFileIsRemovedOnlyWhenItMatchesTheCommittedValue(t *testing.T) {
	root := t.TempDir()
	s := &secretRows{}
	v, e := New(root, root, make([]byte, 32), s)
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	row, _ := v.PrepareSecret(ctx, "token", "dns", "", "synthetic")
	if e = v.FlushSecretsToDisk(ctx, []models.SecretRow{*row}, nil); e != nil {
		t.Fatal(e)
	}
	s.rows = []models.SecretRow{*row}
	if e = v.FlushSecretsToDisk(ctx, nil, []string{"token"}); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(root, "token.secret")); !os.IsNotExist(e) {
		t.Fatal("secret file survived its deletion", e)
	}
}
