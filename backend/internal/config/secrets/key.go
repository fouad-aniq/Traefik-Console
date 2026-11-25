package secrets

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/traefik-console/internal/config/filesystem"
)

// LoadMasterKey preserves the existing private key format; caller holds the state lease.
func LoadMasterKey(keyFile string, requireExisting bool) ([]byte, error) {
	if info, err := os.Lstat(keyFile); err == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return nil, ErrInvalidMasterKey
		}
		raw, err := filesystem.ReadBounded(keyFile, 128)
		if err != nil {
			return nil, ErrInvalidMasterKey
		}
		decoded, err := hex.DecodeString(strings.TrimSpace(string(raw)))
		if err != nil || len(decoded) != 32 {
			return nil, ErrInvalidMasterKey
		}
		return decoded, nil
	} else if !os.IsNotExist(err) {
		return nil, ErrInvalidMasterKey
	}

	if requireExisting {
		return nil, ErrMasterKeyRequired
	}

	// First boot: generate a cryptographically random 32-byte key (AES-256)
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMasterKeyGeneration, err)
	}

	hexKey := hex.EncodeToString(key)

	// Use our atomic writer with 0400 (read-only owner) + directory metadata fsync
	if err := filesystem.WriteFileAtomic(keyFile, []byte(hexKey), 0400); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMasterKeyGeneration, err)
	}

	return key, nil
}
