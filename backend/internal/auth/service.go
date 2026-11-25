// Package auth is the console's single-administrator authentication domain:
// first-run setup, password sign-in, server-side sessions with CSRF tokens,
// external sign-in through OpenID Connect / OAuth 2.0, and the operator CLI
// recovery operations. HTTP concerns live in internal/api/authgate.
package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var (
	ErrSetupRequired      = errors.New("no administrator exists yet; complete setup first")
	ErrSetupComplete      = errors.New("the administrator already exists")
	ErrInvalidSetupToken  = errors.New("invalid setup token")
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrInvalidUsername    = errors.New("username must be 3-64 characters: letters, digits, dot, dash, underscore or @")
	ErrSessionInvalid     = errors.New("session missing, expired or revoked")
	ErrSamePassword       = errors.New("the new password must differ from the current one")
	ErrCurrentSession     = errors.New("use sign out to end the current session")
)

// RateLimitError says when the caller may retry.
type RateLimitError struct{ RetryAfter time.Duration }

func (e *RateLimitError) Error() string { return "too many failed attempts" }

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9._@-]{3,64}$`)

// Config holds operator-tunable behaviour.
type Config struct {
	// IdleTimeout ends a session that has not been used for this long.
	IdleTimeout time.Duration
	// MaxLifetime ends every session this long after sign-in, whatever its activity.
	MaxLifetime time.Duration
	// SecretKey seals the external provider's client secret at rest. It may be
	// nil for CLI use, where the provider secret is never read.
	SecretKey []byte
}

// Client describes the caller for rate limiting and the audit log.
type Client struct {
	IP        string
	UserAgent string
}

// Session is a validated, live session.
type Session struct {
	ID       string
	CSRF     string
	Username string
	Method   string
	// Provider and ProviderName identify the external provider of an "oauth" session.
	Provider     string
	ProviderName string
	CreatedAt    time.Time
	ExpiresAt    time.Time
}

// Issued is a freshly created session together with its bearer value, which
// is only ever placed in an HttpOnly cookie.
type Issued struct {
	Token   string
	Session Session
}

type Service struct {
	store   *Store
	hasher  *hasher
	byIP    *limiter
	byUser  *limiter
	cfg     Config
	now     func() time.Time
	flows   *flowTable
	httpc   providerHTTP
	secrets *secretBox
}

func NewService(store *Store, cfg Config) *Service {
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = 2 * time.Hour
	}
	if cfg.MaxLifetime <= 0 {
		cfg.MaxLifetime = 24 * time.Hour
	}
	if cfg.IdleTimeout > cfg.MaxLifetime {
		cfg.IdleTimeout = cfg.MaxLifetime
	}
	return &Service{
		store:  store,
		hasher: newHasher(),
		// Per client address: 5 failures in 15 minutes lock that address for 15 minutes.
		byIP: newLimiter(5, 15*time.Minute, 15*time.Minute),
		// Per username: a higher bar stops distributed guessing without letting
		// one attacker lock the administrator out for long.
		byUser:  newLimiter(20, 15*time.Minute, 5*time.Minute),
		cfg:     cfg,
		now:     time.Now,
		flows:   newFlowTable(),
		httpc:   newProviderHTTP(),
		secrets: newSecretBox(cfg.SecretKey),
	}
}

// ---------------------------------------------------------------------------
// Status and setup
// ---------------------------------------------------------------------------

// AdminExists reports whether setup has been completed.
func (s *Service) AdminExists(ctx context.Context) (bool, error) {
	var n int
	if err := s.store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin`).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// AdminInfo returns the administrator's username, or "" when setup is pending.
func (s *Service) AdminInfo(ctx context.Context) (string, error) {
	var name string
	err := s.store.db.QueryRowContext(ctx, `SELECT username FROM admin WHERE id=1`).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return name, err
}

// PrepareSetup is called at server start. When no administrator exists it
// mints a fresh one-time setup token (replacing any earlier one) and returns
// it for the operator log; otherwise it removes any stale token and returns "".
func (s *Service) PrepareSetup(ctx context.Context) (string, error) {
	exists, err := s.AdminExists(ctx)
	if err != nil {
		return "", err
	}
	if exists {
		_, err = s.store.db.ExecContext(ctx, `DELETE FROM setup_token`)
		return "", err
	}
	return s.MintSetupToken(ctx)
}

// MintSetupToken replaces the pending setup token. It refuses once an
// administrator exists, so a leaked token can never take over a set-up console.
func (s *Service) MintSetupToken(ctx context.Context) (string, error) {
	exists, err := s.AdminExists(ctx)
	if err != nil {
		return "", err
	}
	if exists {
		return "", ErrSetupComplete
	}
	token := readableSetupToken()
	_, err = s.store.db.ExecContext(ctx,
		`INSERT INTO setup_token(id,token_hash,created_at) VALUES(1,?,?) ON CONFLICT(id) DO UPDATE SET token_hash=excluded.token_hash, created_at=excluded.created_at`,
		hashToken(normalizeSetupToken(token)), s.now().Unix())
	if err != nil {
		return "", err
	}
	return token, nil
}

// SetupInput creates the administrator through the browser.
type SetupInput struct {
	SetupToken string
	Username   string
	Password   string
}

// ValidateUsername applies the username policy.
func ValidateUsername(username string) error {
	if !usernamePattern.MatchString(username) {
		return ErrInvalidUsername
	}
	return nil
}

// Setup creates the single administrator with the one-time token printed in
// the server log, consumes the token and signs the administrator in.
func (s *Service) Setup(ctx context.Context, in SetupInput, client Client) (*Issued, error) {
	username := strings.TrimSpace(in.Username)
	if err := ValidateUsername(username); err != nil {
		return nil, err
	}
	if err := ValidatePassword(in.Password); err != nil {
		return nil, err
	}
	ipKey := "setup:" + client.IP
	if blocked, wait := s.byIP.blocked(ipKey); blocked {
		return nil, &RateLimitError{RetryAfter: wait}
	}
	exists, err := s.AdminExists(ctx)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, ErrSetupComplete
	}
	if !s.setupTokenMatches(ctx, in.SetupToken) {
		s.byIP.fail(ipKey)
		s.auditf(ctx, "setup", "failure", client.IP, "invalid setup token")
		return nil, ErrInvalidSetupToken
	}
	hash, err := s.hasher.hash(ctx, in.Password)
	if err != nil {
		return nil, err
	}
	tx, err := s.store.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// Re-check inside the transaction: two concurrent setups cannot both win.
	var n int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin`).Scan(&n); err != nil {
		return nil, err
	}
	if n > 0 {
		return nil, ErrSetupComplete
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM setup_token WHERE token_hash=?`, hashToken(normalizeSetupToken(in.SetupToken)))
	if err != nil {
		return nil, err
	}
	if consumed, _ := res.RowsAffected(); consumed != 1 {
		return nil, ErrInvalidSetupToken
	}
	now := s.now().Unix()
	if _, err = tx.ExecContext(ctx, `INSERT INTO admin(id,username,password_hash,credential_epoch,created_at,updated_at) VALUES(1,?,?,1,?,?)`, username, hash, now, now); err != nil {
		return nil, err
	}
	issued, err := s.insertSession(ctx, tx, username, 1, "password", "", client)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	s.byIP.reset(ipKey)
	s.auditf(ctx, "setup", "success", client.IP, "administrator "+username+" created")
	return issued, nil
}

func (s *Service) setupTokenMatches(ctx context.Context, raw string) bool {
	token := normalizeSetupToken(raw)
	if token == "" || len(token) > 128 {
		return false
	}
	var stored string
	if err := s.store.db.QueryRowContext(ctx, `SELECT token_hash FROM setup_token WHERE id=1`).Scan(&stored); err != nil {
		return false
	}
	return equalConstantTime(stored, hashToken(token))
}

// ---------------------------------------------------------------------------
// Password sign-in
// ---------------------------------------------------------------------------

func (s *Service) Login(ctx context.Context, username, password string, client Client) (*Issued, error) {
	username = strings.TrimSpace(username)
	ipKey, userKey := "login:"+client.IP, "user:"+strings.ToLower(username)
	if blocked, wait := s.byIP.blocked(ipKey); blocked {
		return nil, &RateLimitError{RetryAfter: wait}
	}
	if blocked, wait := s.byUser.blocked(userKey); blocked {
		return nil, &RateLimitError{RetryAfter: wait}
	}
	var storedName, hash string
	var epoch int64
	err := s.store.db.QueryRowContext(ctx, `SELECT username,password_hash,credential_epoch FROM admin WHERE id=1`).Scan(&storedName, &hash, &epoch)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSetupRequired
	}
	if err != nil {
		return nil, err
	}
	nameMatches := strings.EqualFold(storedName, username)
	if !nameMatches {
		// Burn equivalent work so response timing does not reveal the username.
		hash = dummyHash
	}
	ok, err := s.hasher.verify(ctx, hash, password)
	if err != nil && !errors.Is(err, errInvalidHash) {
		return nil, err
	}
	if !ok || !nameMatches {
		s.byIP.fail(ipKey)
		s.byUser.fail(userKey)
		s.auditf(ctx, "login", "failure", client.IP, "invalid credentials")
		return nil, ErrInvalidCredentials
	}
	tx, err := s.store.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// The credential may have changed while the hash was being checked.
	var current int64
	if err = tx.QueryRowContext(ctx, `SELECT credential_epoch FROM admin WHERE id=1`).Scan(&current); err != nil {
		return nil, err
	}
	if current != epoch {
		return nil, ErrInvalidCredentials
	}
	issued, err := s.insertSession(ctx, tx, storedName, epoch, "password", "", client)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	s.byIP.reset(ipKey)
	s.byUser.reset(userKey)
	s.auditf(ctx, "login", "success", client.IP, "password")
	return issued, nil
}

// ---------------------------------------------------------------------------
// Sessions
// ---------------------------------------------------------------------------

const maxSessions = 20

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func (s *Service) insertSession(ctx context.Context, tx execer, username string, epoch int64, method, provider string, client Client) (*Issued, error) {
	now := s.now()
	token := randomToken(32)
	sess := Session{
		ID: randomToken(12), CSRF: randomToken(32), Username: username, Method: method, Provider: provider,
		CreatedAt: now, ExpiresAt: now.Add(s.cfg.MaxLifetime),
	}
	ua := client.UserAgent
	if len(ua) > 256 {
		ua = ua[:256]
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO sessions(id,token_hash,csrf,credential_epoch,method,provider,created_at,last_seen_at,expires_at,ip,user_agent) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		sess.ID, hashToken(token), sess.CSRF, epoch, method, provider, now.Unix(), now.Unix(), sess.ExpiresAt.Unix(), client.IP, ua); err != nil {
		return nil, err
	}
	// Drop expired sessions and keep only the newest maxSessions.
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ? OR last_seen_at <= ?`, now.Unix(), now.Add(-s.cfg.IdleTimeout).Unix()); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE id IN (SELECT id FROM sessions ORDER BY created_at DESC, rowid DESC LIMIT -1 OFFSET ?)`, maxSessions); err != nil {
		return nil, err
	}
	return &Issued{Token: token, Session: sess}, nil
}

// Authenticate resolves a session cookie value. A session is valid only while
// the administrator exists with the same credential epoch, before its absolute
// expiry and within the idle window. Activity slides the idle window.
func (s *Service) Authenticate(ctx context.Context, token string) (*Session, error) {
	if token == "" || len(token) > 128 {
		return nil, ErrSessionInvalid
	}
	var sess Session
	var created, lastSeen, expires, sessEpoch, adminEpoch int64
	err := s.store.db.QueryRowContext(ctx, `
		SELECT s.id, s.csrf, s.method, s.provider, COALESCE(p.display_name, ''), s.created_at, s.last_seen_at, s.expires_at, s.credential_epoch, a.credential_epoch, a.username
		FROM sessions s JOIN admin a ON a.id = 1 LEFT JOIN oauth_providers p ON p.id = s.provider
		WHERE s.token_hash = ?`, hashToken(token)).
		Scan(&sess.ID, &sess.CSRF, &sess.Method, &sess.Provider, &sess.ProviderName, &created, &lastSeen, &expires, &sessEpoch, &adminEpoch, &sess.Username)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSessionInvalid
	}
	if err != nil {
		return nil, err
	}
	now := s.now()
	if sessEpoch != adminEpoch || now.Unix() >= expires || now.Sub(time.Unix(lastSeen, 0)) >= s.cfg.IdleTimeout {
		_, _ = s.store.db.ExecContext(ctx, `DELETE FROM sessions WHERE id=?`, sess.ID)
		return nil, ErrSessionInvalid
	}
	if now.Unix()-lastSeen >= 60 {
		_, _ = s.store.db.ExecContext(ctx, `UPDATE sessions SET last_seen_at=? WHERE id=?`, now.Unix(), sess.ID)
		lastSeen = now.Unix()
	}
	sess.CreatedAt = time.Unix(created, 0)
	sess.ExpiresAt = time.Unix(min(expires, lastSeen+int64(s.cfg.IdleTimeout/time.Second)), 0)
	return &sess, nil
}

// Logout ends exactly the presented session.
func (s *Service) Logout(ctx context.Context, token string, client Client) error {
	if token == "" {
		return nil
	}
	_, err := s.store.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash=?`, hashToken(token))
	if err == nil {
		s.auditf(ctx, "logout", "success", client.IP, "")
	}
	return err
}

// SessionInfo describes one active session for the security page.
type SessionInfo struct {
	ID         string    `json:"id"`
	Method     string    `json:"method"`
	Provider   string    `json:"provider"`
	IP         string    `json:"ip"`
	UserAgent  string    `json:"user_agent"`
	SignedInAt time.Time `json:"signed_in_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	Current    bool      `json:"current"`
}

func (s *Service) ListSessions(ctx context.Context, currentID string) ([]SessionInfo, error) {
	now := s.now()
	rows, err := s.store.db.QueryContext(ctx, `
		SELECT s.id, s.method, COALESCE(p.display_name, ''), s.ip, s.user_agent, s.created_at, s.last_seen_at
		FROM sessions s JOIN admin a ON a.id=1 LEFT JOIN oauth_providers p ON p.id = s.provider
		WHERE s.credential_epoch = a.credential_epoch AND s.expires_at > ? AND s.last_seen_at > ?
		ORDER BY s.last_seen_at DESC`, now.Unix(), now.Add(-s.cfg.IdleTimeout).Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SessionInfo{}
	for rows.Next() {
		var info SessionInfo
		var created, seen int64
		if err = rows.Scan(&info.ID, &info.Method, &info.Provider, &info.IP, &info.UserAgent, &created, &seen); err != nil {
			return nil, err
		}
		info.SignedInAt, info.LastSeenAt, info.Current = time.Unix(created, 0).UTC(), time.Unix(seen, 0).UTC(), info.ID == currentID
		out = append(out, info)
	}
	return out, rows.Err()
}

// RevokeSession signs out one session (not the caller's own: that is Logout).
func (s *Service) RevokeSession(ctx context.Context, id, currentID string, client Client) error {
	if id == currentID {
		return ErrCurrentSession
	}
	res, err := s.store.db.ExecContext(ctx, `DELETE FROM sessions WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrSessionInvalid
	}
	s.auditf(ctx, "session_revoked", "success", client.IP, "")
	return nil
}

// RevokeOtherSessions signs out every session but the current one.
func (s *Service) RevokeOtherSessions(ctx context.Context, currentID string, client Client) (int64, error) {
	res, err := s.store.db.ExecContext(ctx, `DELETE FROM sessions WHERE id <> ?`, currentID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	s.auditf(ctx, "sessions_revoked", "success", client.IP, fmt.Sprintf("%d other session(s)", n))
	return n, nil
}

// ---------------------------------------------------------------------------
// Credentials
// ---------------------------------------------------------------------------

// VerifyPassword re-authenticates the signed-in administrator before a
// sensitive change. Failures count toward the same per-address limit as sign-in.
func (s *Service) VerifyPassword(ctx context.Context, password string, client Client) error {
	key := "reauth:" + client.IP
	if blocked, wait := s.byIP.blocked(key); blocked {
		return &RateLimitError{RetryAfter: wait}
	}
	var hash string
	if err := s.store.db.QueryRowContext(ctx, `SELECT password_hash FROM admin WHERE id=1`).Scan(&hash); err != nil {
		return err
	}
	ok, err := s.hasher.verify(ctx, hash, password)
	if err != nil {
		return err
	}
	if !ok {
		s.byIP.fail(key)
		s.auditf(ctx, "reauthenticate", "failure", client.IP, "")
		return ErrInvalidCredentials
	}
	s.byIP.reset(key)
	return nil
}

// ChangePassword replaces the password, ends every session and signs the
// current browser in again with a fresh session.
func (s *Service) ChangePassword(ctx context.Context, current, next string, client Client) (*Issued, error) {
	if err := ValidatePassword(next); err != nil {
		return nil, err
	}
	if current == next {
		return nil, ErrSamePassword
	}
	if err := s.VerifyPassword(ctx, current, client); err != nil {
		return nil, err
	}
	hash, err := s.hasher.hash(ctx, next)
	if err != nil {
		return nil, err
	}
	tx, err := s.store.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	epoch, username, err := replaceCredential(ctx, tx, hash, s.now())
	if err != nil {
		return nil, err
	}
	issued, err := s.insertSession(ctx, tx, username, epoch, "password", "", client)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	s.auditf(ctx, "password_changed", "success", client.IP, "all other sessions ended")
	return issued, nil
}

func replaceCredential(ctx context.Context, tx *sql.Tx, hash string, now time.Time) (int64, string, error) {
	res, err := tx.ExecContext(ctx, `UPDATE admin SET password_hash=?, credential_epoch=credential_epoch+1, updated_at=? WHERE id=1`, hash, now.Unix())
	if err != nil {
		return 0, "", err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return 0, "", ErrSetupRequired
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM sessions`); err != nil {
		return 0, "", err
	}
	var epoch int64
	var username string
	err = tx.QueryRowContext(ctx, `SELECT credential_epoch, username FROM admin WHERE id=1`).Scan(&epoch, &username)
	return epoch, username, err
}

// ---------------------------------------------------------------------------
// Operator CLI operations (run on the host, never reachable over HTTP)
// ---------------------------------------------------------------------------

// CreateAdmin creates the administrator from the command line when none exists.
func (s *Service) CreateAdmin(ctx context.Context, username, password string) error {
	username = strings.TrimSpace(username)
	if err := ValidateUsername(username); err != nil {
		return err
	}
	hash, err := s.hasher.hash(ctx, password)
	if err != nil {
		return err
	}
	tx, err := s.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrSetupComplete
	}
	now := s.now().Unix()
	if _, err = tx.ExecContext(ctx, `INSERT INTO admin(id,username,password_hash,credential_epoch,created_at,updated_at) VALUES(1,?,?,1,?,?)`, username, hash, now, now); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM setup_token`); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.auditf(ctx, "setup", "success", "local-cli", "administrator "+username+" created from the command line")
	return nil
}

// ResetPassword sets a new password from the command line and ends every session.
func (s *Service) ResetPassword(ctx context.Context, password string) error {
	hash, err := s.hasher.hash(ctx, password)
	if err != nil {
		return err
	}
	tx, err := s.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, _, err = replaceCredential(ctx, tx, hash, s.now()); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.auditf(ctx, "password_reset", "success", "local-cli", "all sessions ended")
	return nil
}

// ResetAll removes the administrator, every session and the external sign-in
// provider, returning the console to first-run setup. It returns the new setup token.
func (s *Service) ResetAll(ctx context.Context) (string, error) {
	tx, err := s.store.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	for _, table := range []string{"sessions", "admin", "oauth_providers", "setup_token"} {
		if _, err = tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return "", err
		}
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	s.auditf(ctx, "reset", "success", "local-cli", "administrator and external sign-in removed")
	return s.MintSetupToken(ctx)
}

// ---------------------------------------------------------------------------
// Audit
// ---------------------------------------------------------------------------

// Event is one audit log entry.
type Event struct {
	At      time.Time `json:"at"`
	Event   string    `json:"event"`
	Outcome string    `json:"outcome"`
	IP      string    `json:"ip"`
	Detail  string    `json:"detail"`
}

func (s *Service) RecentEvents(ctx context.Context, limit int) ([]Event, error) {
	rows, err := s.store.db.QueryContext(ctx, `SELECT at,event,outcome,ip,detail FROM audit_log ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		var at int64
		if err = rows.Scan(&at, &e.Event, &e.Outcome, &e.IP, &e.Detail); err != nil {
			return nil, err
		}
		e.At = time.Unix(at, 0).UTC()
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Service) auditf(ctx context.Context, event, outcome, ip, detail string) {
	// Audit writes must not fail a sign-in; a detached context survives client cancellation.
	_ = s.store.audit(context.WithoutCancel(ctx), event, outcome, ip, detail)
}
