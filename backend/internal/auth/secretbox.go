package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
)

var errNoSecretKey = errors.New("client secret encryption key unavailable")

// secretBox seals the provider client secret with a key derived from the
// console master key, so auth.db alone never reveals it.
type secretBox struct{ key []byte }

var secretAAD = []byte("traefik-console/auth/oauth-client-secret/v1")

func newSecretBox(master []byte) *secretBox {
	if len(master) == 0 {
		return &secretBox{}
	}
	mac := hmac.New(sha256.New, master)
	mac.Write([]byte("traefik-console auth secret key v1"))
	return &secretBox{key: mac.Sum(nil)}
}

func (b *secretBox) seal(plain string) ([]byte, error) {
	if len(b.key) == 0 {
		return nil, errNoSecretKey
	}
	block, err := aes.NewCipher(b.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, []byte(plain), secretAAD), nil
}

func (b *secretBox) open(sealed []byte) (string, error) {
	if len(sealed) == 0 {
		return "", nil
	}
	if len(b.key) == 0 {
		return "", errNoSecretKey
	}
	block, err := aes.NewCipher(b.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(sealed) < gcm.NonceSize() {
		return "", errors.New("sealed client secret is truncated")
	}
	plain, err := gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], secretAAD)
	if err != nil {
		return "", errors.New("client secret cannot be decrypted with the current master key")
	}
	return string(plain), nil
}
