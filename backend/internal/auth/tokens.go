package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/hex"
	"strings"
)

// randomToken returns n random bytes, hex encoded.
func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand never fails on supported platforms; refuse to continue with weak tokens.
		panic("auth: crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// SetupTokenPrefix marks console setup tokens ("Traefik Console Setup") so they
// are easy to tell apart from other secrets in a log.
const SetupTokenPrefix = "TCS"

// readableSetupToken returns 80 random bits as TCS-XXXX-XXXX-XXXX-XXXX
// (base32: A–Z and 2–7, no 0/1/8/9). Plenty for a single-use, rate-limited
// token that is replaced on every restart, and short enough to type.
func readableSetupToken() string {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		panic("auth: crypto/rand unavailable: " + err.Error())
	}
	s := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b) // 16 characters
	return SetupTokenPrefix + "-" + s[0:4] + "-" + s[4:8] + "-" + s[8:12] + "-" + s[12:16]
}

// normalizeSetupToken accepts the token with or without the prefix and dashes,
// in any case: "tcs-abcd-…", "ABCD EFGH …" and "TCSABCD…" all match.
func normalizeSetupToken(raw string) string {
	s := strings.ToUpper(strings.NewReplacer("-", "", " ", "", "\t", "").Replace(strings.TrimSpace(raw)))
	if len(s) == 16+len(SetupTokenPrefix) && strings.HasPrefix(s, SetupTokenPrefix) {
		s = s[len(SetupTokenPrefix):]
	}
	return s
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func equalConstantTime(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
