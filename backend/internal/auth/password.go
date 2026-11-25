package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const (
	MinPasswordLength = 12
	MaxPasswordLength = 128
)

var (
	ErrPasswordTooShort = fmt.Errorf("password must be at least %d characters", MinPasswordLength)
	ErrPasswordTooLong  = fmt.Errorf("password cannot exceed %d characters", MaxPasswordLength)
	ErrBusy             = errors.New("authentication is busy, retry shortly")
	errInvalidHash      = errors.New("invalid password hash")
)

// argon2id parameters: 64 MiB, 3 passes, 1 lane, 32-byte key (OWASP baseline).
const (
	argonMemory  = 64 * 1024
	argonTime    = 3
	argonThreads = 1
	argonKeyLen  = 32
	argonSaltLen = 16
)

// hasher bounds concurrent Argon2id work so a burst of sign-in attempts cannot
// exhaust memory: at most two evaluations run at once, others wait briefly.
type hasher struct{ slots chan struct{} }

func newHasher() *hasher { return &hasher{slots: make(chan struct{}, 2)} }

func (h *hasher) acquire(ctx context.Context) (func(), error) {
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	select {
	case h.slots <- struct{}{}:
		return func() { <-h.slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, ErrBusy
	}
}

// ValidatePassword enforces the length policy (counted in characters).
func ValidatePassword(password string) error {
	n := utf8.RuneCountInString(password)
	if n < MinPasswordLength {
		return ErrPasswordTooShort
	}
	if n > MaxPasswordLength || len(password) > 4*MaxPasswordLength {
		return ErrPasswordTooLong
	}
	return nil
}

func (h *hasher) hash(ctx context.Context, password string) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}
	release, err := h.acquire(ctx)
	if err != nil {
		return "", err
	}
	defer release()
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// verify compares in constant time. Stored parameters are bounded before any
// allocation so a corrupted row cannot request gigabytes of memory.
func (h *hasher) verify(ctx context.Context, encoded, password string) (bool, error) {
	if len(password) > 4*MaxPasswordLength {
		return false, nil
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != fmt.Sprintf("v=%d", argon2.Version) {
		return false, errInvalidHash
	}
	var memory, iterations uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &threads); err != nil {
		return false, errInvalidHash
	}
	if memory < 8*1024 || memory > 256*1024 || iterations < 1 || iterations > 16 || threads < 1 || threads > 8 {
		return false, errInvalidHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 8 || len(salt) > 64 {
		return false, errInvalidHash
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(key) < 16 || len(key) > 64 {
		return false, errInvalidHash
	}
	release, err := h.acquire(ctx)
	if err != nil {
		return false, err
	}
	defer release()
	computed := argon2.IDKey([]byte(password), salt, iterations, memory, threads, uint32(len(key)))
	return subtle.ConstantTimeCompare(key, computed) == 1, nil
}

// dummyHash keeps the timing of an unknown-username sign-in equal to a real one.
const dummyHash = "$argon2id$v=19$m=65536,t=3,p=1$c29tZXNhbHR2YWx1ZTE2$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
