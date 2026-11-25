package middlewares

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// Basic Auth users are entered as user name + password and hashed here with bcrypt (the strongest
// format Traefik accepts). Plaintext passwords never reach the stored resource, a file or a log:
// only the resulting htpasswd lines are kept, in the middleware's 0600 users file.

const (
	credentialsKey     = "users_credentials"
	maxCredentialUsers = 100
	bcryptCost         = bcrypt.DefaultCost
)

var (
	usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9_.@-]{1,64}$`)

	ErrInvalidCredentials = errors.New("invalid basic auth users")
)

// Credential is one Basic Auth user as entered in the form.
type Credential struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// parseCredentials reads the users_credentials list of a basicAuth spec (JSON-decoded).
func parseCredentials(raw any) ([]Credential, error) {
	items, ok := raw.([]any)
	if !ok || len(items) == 0 || len(items) > maxCredentialUsers {
		return nil, fmt.Errorf("%w: between 1 and %d users are required", ErrInvalidCredentials, maxCredentialUsers)
	}
	out := make([]Credential, 0, len(items))
	seen := map[string]bool{}
	for i, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%w: user %d is malformed", ErrInvalidCredentials, i+1)
		}
		name, _ := obj["username"].(string)
		password, _ := obj["password"].(string)
		switch {
		case !usernamePattern.MatchString(name):
			return nil, fmt.Errorf("%w: user %d has an invalid name", ErrInvalidCredentials, i+1)
		case seen[name]:
			return nil, fmt.Errorf("%w: user name %q is repeated", ErrInvalidCredentials, name)
		case len(password) < 1 || len(password) > 72 || strings.ContainsAny(password, "\x00\r\n"):
			// 72 bytes is bcrypt's limit; longer passwords would be silently truncated.
			return nil, fmt.Errorf("%w: the password of %q must be 1 to 72 bytes on one line", ErrInvalidCredentials, name)
		}
		seen[name] = true
		out = append(out, Credential{Username: name, Password: password})
	}
	return out, nil
}

// htpasswdLines hashes every password with bcrypt and returns "user:hash" lines.
func htpasswdLines(users []Credential) (string, error) {
	lines := make([]string, 0, len(users))
	for _, u := range users {
		hash, err := bcrypt.GenerateFromPassword([]byte(u.Password), bcryptCost)
		if err != nil {
			return "", fmt.Errorf("%w: %v", ErrInvalidCredentials, err)
		}
		lines = append(lines, u.Username+":"+string(hash))
	}
	return strings.Join(lines, "\n") + "\n", nil
}
