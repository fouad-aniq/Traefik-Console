package db

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
)

// sealAAD is the associated data for every sealed blob. It is deliberately
// unchanged from the original journal so existing rows stay readable.
var sealAAD = []byte("core-v2-journal-v1")

var (
	ErrJournalCapacity = errors.New("encrypted journal capacity reached; operator backup and capacity review required")
	ErrSnapshotQuota   = errors.New("snapshot storage quota reached; delete snapshots or raise MAX_SNAPSHOT_BYTES")
)

func (s *sqlStore) seal(b []byte) ([]byte, error) {
	if b == nil {
		return nil, nil
	}
	if len(s.key) != 32 {
		return nil, fmt.Errorf("journal encryption key required")
	}
	block, e := aes.NewCipher(s.key)
	if e != nil {
		return nil, e
	}
	g, e := cipher.NewGCM(block)
	if e != nil {
		return nil, e
	}
	nonce := make([]byte, g.NonceSize())
	if _, e = rand.Read(nonce); e != nil {
		return nil, e
	}
	return g.Seal(nonce, nonce, b, sealAAD), nil
}

func (s *sqlStore) open(b []byte) ([]byte, error) {
	if len(b) == 0 {
		return nil, nil
	}
	block, e := aes.NewCipher(s.key)
	if e != nil {
		return nil, e
	}
	g, e := cipher.NewGCM(block)
	if e != nil {
		return nil, e
	}
	if len(b) < g.NonceSize() {
		return nil, fmt.Errorf("invalid journal ciphertext")
	}
	return g.Open(nil, b[:g.NonceSize()], b[g.NonceSize():], sealAAD)
}
