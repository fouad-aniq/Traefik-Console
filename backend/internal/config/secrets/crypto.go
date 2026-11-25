package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"io"
)

func (m *Vault) encrypt(plaintext string) ([]byte, error) {
	block, err := aes.NewCipher(m.masterKey)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

func (m *Vault) decrypt(ciphertext []byte) (string, error) {
	block, err := aes.NewCipher(m.masterKey)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(ciphertext) < gcm.NonceSize() {
		return "", ErrDecryptionFailed
	}

	nonce, data := ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, data, nil)
	if err != nil {
		return "", ErrDecryptionFailed
	}
	return string(plain), nil
}

func (m *Vault) SameValue(a, b []byte) (bool, error) {
	x, e := m.decrypt(a)
	if e != nil {
		return false, e
	}
	y, e := m.decrypt(b)
	if e != nil {
		return false, e
	}
	return x == y, nil
}
