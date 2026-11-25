package secrets

import "errors"

var (
	ErrSecretNotFound   = errors.New("requested secret handle was not found")
	ErrSecretEmptyValue = errors.New("secret value cannot be empty")
	ErrDecryptionFailed = errors.New("failed to decrypt secret payload with master key")
)

var ErrRuntimeConflict = errors.New("registered runtime secret changed externally")

var (
	ErrInvalidMasterKey    = errors.New("master key file is corrupted or does not contain a valid 32-byte hex key")
	ErrMasterKeyGeneration = errors.New("failed to generate or persist master encryption key")
	ErrMasterKeyRequired   = errors.New("existing database requires the original master key")
)
