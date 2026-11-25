package auth

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const goodPassword = "correct horse battery"

func newTestService(t *testing.T) (*Service, *Store) {
	t.Helper()
	store, err := OpenStore(filepath.Join(privateDir(t), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	svc := NewService(store, Config{IdleTimeout: time.Hour, MaxLifetime: 4 * time.Hour, SecretKey: make([]byte, 32)})
	return svc, store
}

func setupAdmin(t *testing.T, svc *Service) *Issued {
	t.Helper()
	ctx := context.Background()
	token, err := svc.PrepareSetup(ctx)
	if err != nil || token == "" {
		t.Fatalf("PrepareSetup = %q, %v", token, err)
	}
	issued, err := svc.Setup(ctx, SetupInput{SetupToken: token, Username: "admin", Password: goodPassword}, Client{IP: "10.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	return issued
}

func TestSetupRequiresTheOneTimeToken(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	token, err := svc.PrepareSetup(ctx)
	if err != nil || len(token) != 23 || !strings.HasPrefix(token, "TCS-") {
		t.Fatalf("token %q err %v", token, err)
	}
	if _, err = svc.Setup(ctx, SetupInput{SetupToken: "WRONG-TOKEN", Username: "admin", Password: goodPassword}, Client{IP: "1.1.1.1"}); !errors.Is(err, ErrInvalidSetupToken) {
		t.Fatalf("wrong token: %v", err)
	}
	if _, err = svc.Setup(ctx, SetupInput{SetupToken: token, Username: "a", Password: goodPassword}, Client{}); !errors.Is(err, ErrInvalidUsername) {
		t.Fatalf("short username: %v", err)
	}
	if _, err = svc.Setup(ctx, SetupInput{SetupToken: token, Username: "admin", Password: "short"}, Client{}); !errors.Is(err, ErrPasswordTooShort) {
		t.Fatalf("short password: %v", err)
	}
	// Tokens are accepted without dashes and in lower case (copy/paste friendliness).
	// Accepted without the prefix and dashes, in lower case (copy/paste and typing friendliness).
	relaxed := strings.ToLower(strings.ReplaceAll(strings.TrimPrefix(token, "TCS-"), "-", ""))
	issued, err := svc.Setup(ctx, SetupInput{SetupToken: relaxed, Username: "admin", Password: goodPassword}, Client{})
	if err != nil {
		t.Fatal(err)
	}
	if issued.Token == "" || issued.Session.CSRF == "" || issued.Session.Username != "admin" {
		t.Fatalf("unexpected session %+v", issued.Session)
	}
	// Once an administrator exists, setup is closed for good, even with the same token.
	if _, err = svc.Setup(ctx, SetupInput{SetupToken: token, Username: "other", Password: goodPassword}, Client{}); !errors.Is(err, ErrSetupComplete) {
		t.Fatalf("second setup: %v", err)
	}
	if _, err = svc.MintSetupToken(ctx); !errors.Is(err, ErrSetupComplete) {
		t.Fatalf("mint after setup: %v", err)
	}
	if again, err := svc.PrepareSetup(ctx); err != nil || again != "" {
		t.Fatalf("PrepareSetup after setup = %q, %v", again, err)
	}
}

func TestSetupTokenIsReplacedOnRestart(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	first, _ := svc.PrepareSetup(ctx)
	second, _ := svc.PrepareSetup(ctx)
	if first == second {
		t.Fatal("expected a fresh token")
	}
	if _, err := svc.Setup(ctx, SetupInput{SetupToken: first, Username: "admin", Password: goodPassword}, Client{}); !errors.Is(err, ErrInvalidSetupToken) {
		t.Fatalf("superseded token accepted: %v", err)
	}
}

func TestSetupIsRateLimited(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	token, _ := svc.PrepareSetup(ctx)
	for range 5 {
		_, _ = svc.Setup(ctx, SetupInput{SetupToken: "nope", Username: "admin", Password: goodPassword}, Client{IP: "9.9.9.9"})
	}
	var rate *RateLimitError
	if _, err := svc.Setup(ctx, SetupInput{SetupToken: token, Username: "admin", Password: goodPassword}, Client{IP: "9.9.9.9"}); !errors.As(err, &rate) {
		t.Fatalf("expected rate limit, got %v", err)
	}
	if _, err := svc.Setup(ctx, SetupInput{SetupToken: token, Username: "admin", Password: goodPassword}, Client{IP: "9.9.9.8"}); err != nil {
		t.Fatalf("other address blocked: %v", err)
	}
}

func TestLoginAndRateLimit(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	if _, err := svc.Login(ctx, "admin", goodPassword, Client{}); !errors.Is(err, ErrSetupRequired) {
		t.Fatalf("login before setup: %v", err)
	}
	setupAdmin(t, svc)
	if _, err := svc.Login(ctx, "ADMIN", goodPassword, Client{IP: "2.2.2.2"}); err != nil {
		t.Fatalf("case-insensitive username: %v", err)
	}
	if _, err := svc.Login(ctx, "nobody", goodPassword, Client{IP: "3.3.3.3"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("unknown user: %v", err)
	}
	for range 4 {
		if _, err := svc.Login(ctx, "admin", "wrong password!!", Client{IP: "3.3.3.3"}); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("wrong password: %v", err)
		}
	}
	var rate *RateLimitError
	if _, err := svc.Login(ctx, "admin", goodPassword, Client{IP: "3.3.3.3"}); !errors.As(err, &rate) || rate.RetryAfter <= 0 {
		t.Fatalf("expected lockout, got %v", err)
	}
}

func TestSessionLifecycle(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	issued := setupAdmin(t, svc)
	sess, err := svc.Authenticate(ctx, issued.Token)
	if err != nil || sess.CSRF != issued.Session.CSRF {
		t.Fatalf("authenticate: %v", err)
	}
	if _, err = svc.Authenticate(ctx, issued.Token+"x"); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("tampered token: %v", err)
	}

	// Idle timeout.
	base := time.Now()
	svc.now = func() time.Time { return base.Add(61 * time.Minute) }
	if _, err = svc.Authenticate(ctx, issued.Token); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("idle session accepted: %v", err)
	}

	// Absolute lifetime, even when active.
	svc.now = func() time.Time { return base }
	active, _ := svc.Login(ctx, "admin", goodPassword, Client{})
	for i := 1; i <= 5; i++ {
		at := base.Add(time.Duration(i) * 50 * time.Minute)
		svc.now = func() time.Time { return at }
		_, err = svc.Authenticate(ctx, active.Token)
		if i < 5 && err != nil {
			t.Fatalf("active session ended early at step %d: %v", i, err)
		}
	}
	if !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("session outlived its maximum lifetime: %v", err)
	}
}

func TestPasswordChangeEndsOtherSessions(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	first := setupAdmin(t, svc)
	second, _ := svc.Login(ctx, "admin", goodPassword, Client{})
	if _, err := svc.ChangePassword(ctx, "not the password", "another long password", Client{}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong current password: %v", err)
	}
	if _, err := svc.ChangePassword(ctx, goodPassword, goodPassword, Client{}); !errors.Is(err, ErrSamePassword) {
		t.Fatalf("same password: %v", err)
	}
	fresh, err := svc.ChangePassword(ctx, goodPassword, "another long password", Client{})
	if err != nil {
		t.Fatal(err)
	}
	for _, old := range []*Issued{first, second} {
		if _, err = svc.Authenticate(ctx, old.Token); !errors.Is(err, ErrSessionInvalid) {
			t.Fatalf("old session survived: %v", err)
		}
	}
	if _, err = svc.Authenticate(ctx, fresh.Token); err != nil {
		t.Fatalf("fresh session: %v", err)
	}
	if _, err = svc.Login(ctx, "admin", "another long password", Client{}); err != nil {
		t.Fatalf("login with new password: %v", err)
	}
}

func TestRevokeOtherSessions(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	keep := setupAdmin(t, svc)
	other, _ := svc.Login(ctx, "admin", goodPassword, Client{})
	list, _ := svc.ListSessions(ctx, keep.Session.ID)
	if len(list) != 2 {
		t.Fatalf("sessions %d", len(list))
	}
	if n, err := svc.RevokeOtherSessions(ctx, keep.Session.ID, Client{}); err != nil || n != 1 {
		t.Fatalf("revoked %d %v", n, err)
	}
	if _, err := svc.Authenticate(ctx, other.Token); !errors.Is(err, ErrSessionInvalid) {
		t.Fatal("other session survived")
	}
	if _, err := svc.Authenticate(ctx, keep.Token); err != nil {
		t.Fatal("current session ended")
	}
}

func TestCommandLineOperations(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	setupToken, _ := svc.PrepareSetup(ctx)
	if err := svc.ResetPassword(ctx, goodPassword); !errors.Is(err, ErrSetupRequired) {
		t.Fatalf("reset before create: %v", err)
	}
	if err := svc.CreateAdmin(ctx, "root", goodPassword); err != nil {
		t.Fatal(err)
	}
	// The browser token printed earlier is now useless.
	if _, err := svc.Setup(ctx, SetupInput{SetupToken: setupToken, Username: "x-admin", Password: goodPassword}, Client{}); !errors.Is(err, ErrSetupComplete) {
		t.Fatalf("setup after CLI create: %v", err)
	}
	if err := svc.CreateAdmin(ctx, "root2", goodPassword); !errors.Is(err, ErrSetupComplete) {
		t.Fatalf("second create: %v", err)
	}
	sess, _ := svc.Login(ctx, "root", goodPassword, Client{})
	if err := svc.ResetPassword(ctx, "a brand new password"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(ctx, sess.Token); !errors.Is(err, ErrSessionInvalid) {
		t.Fatal("session survived a CLI password reset")
	}
	token, err := svc.ResetAll(ctx)
	if err != nil || token == "" {
		t.Fatalf("reset all: %q %v", token, err)
	}
	if exists, _ := svc.AdminExists(ctx); exists {
		t.Fatal("administrator survived reset")
	}
	if _, err = svc.Setup(ctx, SetupInput{SetupToken: token, Username: "admin", Password: goodPassword}, Client{}); err != nil {
		t.Fatalf("setup after reset: %v", err)
	}
}

func TestStoreRefusesSharedDirectory(t *testing.T) {
	dir := privateDir(t)
	if err := chmod(dir, 0775); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(filepath.Join(dir, "auth.db")); err == nil {
		t.Fatal("group-writable auth directory accepted")
	}
}

func TestStoreRefusesSharedFile(t *testing.T) {
	dir := privateDir(t)
	path := filepath.Join(dir, "auth.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if err = chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = OpenStore(path); err == nil {
		t.Fatal("world-readable auth database accepted")
	}
}

func TestSetupTokenFormat(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		tok := readableSetupToken()
		if len(tok) != 23 || !strings.HasPrefix(tok, "TCS-") || strings.Count(tok, "-") != 4 || strings.ContainsAny(tok, "0189") {
			t.Fatalf("unexpected token %q", tok)
		}
		if seen[tok] {
			t.Fatal("duplicate token")
		}
		seen[tok] = true
		want := normalizeSetupToken(tok)
		for _, variant := range []string{strings.ToLower(tok), strings.TrimPrefix(tok, "TCS-"), strings.ReplaceAll(tok, "-", ""), " " + tok + " "} {
			if normalizeSetupToken(variant) != want {
				t.Fatalf("%q does not normalize like %q", variant, tok)
			}
		}
	}
	// A 16-character body that happens to start with TCS is not mistaken for a prefixed token.
	if normalizeSetupToken("TCSABCDEFGHIJKLM") != "TCSABCDEFGHIJKLM" {
		t.Fatal("bare token starting with TCS was truncated")
	}
}

func TestRevokeOneSession(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	me := setupAdmin(t, svc)
	other, _ := svc.Login(ctx, "admin", goodPassword, Client{})
	if err := svc.RevokeSession(ctx, me.Session.ID, me.Session.ID, Client{}); !errors.Is(err, ErrCurrentSession) {
		t.Fatalf("revoking own session: %v", err)
	}
	if err := svc.RevokeSession(ctx, other.Session.ID, me.Session.ID, Client{}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(ctx, other.Token); !errors.Is(err, ErrSessionInvalid) {
		t.Fatal("revoked session still valid")
	}
	if err := svc.RevokeSession(ctx, other.Session.ID, me.Session.ID, Client{}); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("second revoke: %v", err)
	}
}

// An auth.db written by the first release (single provider table) upgrades in place.
func TestUpgradeFromSchemaV1(t *testing.T) {
	path := filepath.Join(privateDir(t), "auth.db")
	raw, err := sql.Open(driverName, "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE auth_schema (version INTEGER NOT NULL)`, `INSERT INTO auth_schema(version) VALUES(1)`, schema[0],
		`INSERT INTO admin VALUES(1,'admin','x',1,1,1)`,
		`INSERT INTO oauth_provider VALUES(1,1,'oidc','Authentik','https://id.example.com','','','','console',NULL,'["openid"]','email','["a@example.com"]','https://c.example.com/api/v1/auth/oauth/callback',3,1)`,
		`INSERT INTO sessions(id,token_hash,csrf,credential_epoch,method,created_at,last_seen_at,expires_at) VALUES('s1','h','c',1,'oauth',1,1,1)`,
	} {
		if _, err = raw.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	raw.Close()
	if err = chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	svc := NewService(store, Config{SecretKey: make([]byte, 32)})
	list, err := svc.Providers(context.Background())
	if err != nil || len(list) != 1 || list[0].ID != "default" || list[0].DisplayName != "Authentik" {
		t.Fatalf("migrated providers %+v %v", list, err)
	}
	var provider string
	store.db.QueryRow(`SELECT provider FROM sessions WHERE id='s1'`).Scan(&provider)
	if provider != "default" {
		t.Fatalf("session provider %q", provider)
	}
}
