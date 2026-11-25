package middlewares

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/models"
	"golang.org/x/crypto/bcrypt"
)

// captureSecrets records the secret the manager prepares instead of encrypting it.
type captureSecrets struct{ values map[string]string }

func (c *captureSecrets) PrepareSecret(_ context.Context, name, purpose, env, value string) (*models.SecretRow, error) {
	c.values[name] = value
	return &models.SecretRow{Name: name, Purpose: purpose, FilePath: "/etc/traefik/secrets/" + name + ".secret"}, nil
}
func (c *captureSecrets) DecryptSecret(context.Context, string) (string, error) { return "", nil }

type captureApplier struct{ applied []models.Mutation }

func (a *captureApplier) Apply(_ context.Context, m models.Mutation) (*models.OperationResult, error) {
	a.applied = append(a.applied, m)
	return &models.OperationResult{Operation: &models.OperationRow{Status: models.OpStatusSucceeded}}, nil
}
func (a *captureApplier) Preview(context.Context, models.Mutation) (*models.OperationPreview, error) {
	return &models.OperationPreview{}, nil
}

type emptyStore struct{ db.ItemReader }

func (emptyStore) GetItem(context.Context, models.ItemKind, string) (*models.ConfigItemRow, error) {
	return nil, nil
}

func credentialsSpec(users ...[2]string) map[string]any {
	list := []any{}
	for _, u := range users {
		list = append(list, map[string]any{"username": u[0], "password": u[1]})
	}
	return map[string]any{credentialsKey: list}
}

func TestBasicAuthUsersAreHashedWithBcryptAndNeverStoredInPlaintext(t *testing.T) {
	secrets := &captureSecrets{values: map[string]string{}}
	applier := &captureApplier{}
	m := &middlewareManager{store: emptyStore{}, secrets: secrets, applier: applier}
	_, _ = m.UpsertMiddleware(context.Background(), "auth", models.MiddlewareConfig{Type: "basicAuth", Spec: credentialsSpec([2]string{"admin", "s3cret pass"}, [2]string{"ops.team", "other"})}, models.WriteOptions{})
	if len(applier.applied) != 1 {
		t.Fatal("no operation applied")
	}
	mut := applier.applied[0]
	payload := string(mut.Upserts[0].Payload)
	for _, leaked := range []string{"s3cret pass", "other", credentialsKey, "users_htpasswd"} {
		if strings.Contains(payload, leaked) {
			t.Fatalf("stored resource contains %q: %s", leaked, payload)
		}
	}
	var cfg models.MiddlewareConfig
	_ = json.Unmarshal(mut.Upserts[0].Payload, &cfg)
	if cfg.Spec["usersFile"] != "/etc/traefik/secrets/basicauth_auth.secret" {
		t.Fatal("users file not referenced", cfg.Spec)
	}
	lines := strings.Split(strings.TrimSpace(secrets.values["basicauth_auth"]), "\n")
	if len(lines) != 2 {
		t.Fatal("htpasswd lines", lines)
	}
	for i, want := range [][2]string{{"admin", "s3cret pass"}, {"ops.team", "other"}} {
		user, hash, _ := strings.Cut(lines[i], ":")
		if user != want[0] || !strings.HasPrefix(hash, "$2") || bcrypt.CompareHashAndPassword([]byte(hash), []byte(want[1])) != nil {
			t.Fatalf("line %d is not a bcrypt hash of the password: %q", i, lines[i])
		}
		if strings.Contains(lines[i], want[1]) {
			t.Fatal("plaintext password in the users file")
		}
	}
}

func TestBasicAuthUsersRejected(t *testing.T) {
	long := strings.Repeat("x", 73)
	for name, spec := range map[string]map[string]any{
		"no users":         {credentialsKey: []any{}},
		"bad name":         credentialsSpec([2]string{"bad name", "pw"}),
		"colon in name":    credentialsSpec([2]string{"a:b", "pw"}),
		"repeated name":    credentialsSpec([2]string{"a", "pw"}, [2]string{"a", "pw2"}),
		"empty password":   credentialsSpec([2]string{"a", ""}),
		"over 72 bytes":    credentialsSpec([2]string{"a", long}),
		"newline password": credentialsSpec([2]string{"a", "x\ny"}),
		"not a list":       {credentialsKey: "admin:pw"},
		"both formats":     {credentialsKey: credentialsSpec([2]string{"a", "pw"})[credentialsKey], "users_htpasswd": "a:$2y$x"},
	} {
		t.Run(name, func(t *testing.T) {
			secrets := &captureSecrets{values: map[string]string{}}
			applier := &captureApplier{}
			m := &middlewareManager{store: emptyStore{}, secrets: secrets, applier: applier}
			_, err := m.UpsertMiddleware(context.Background(), "auth", models.MiddlewareConfig{Type: "basicAuth", Spec: spec}, models.WriteOptions{})
			if !errors.Is(err, ErrInvalidCredentials) || len(applier.applied) != 0 || len(secrets.values) != 0 {
				t.Fatal("accepted or partially written", err)
			}
			if strings.Contains(err.Error(), "pw") || strings.Contains(err.Error(), long) {
				t.Fatal("error message leaks a password", err)
			}
		})
	}
}

func TestCredentialsAreForBasicAuthOnly(t *testing.T) {
	m := &middlewareManager{store: emptyStore{}, secrets: &captureSecrets{values: map[string]string{}}, applier: &captureApplier{}}
	if _, err := m.UpsertMiddleware(context.Background(), "d", models.MiddlewareConfig{Type: "digestAuth", Spec: credentialsSpec([2]string{"a", "pw"})}, models.WriteOptions{}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatal(err)
	}
}
