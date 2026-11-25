package certificates

import "errors"

var (
	ErrCertInvalidPEM  = errors.New("invalid PEM certificate chain format")
	ErrCertInvalidKey  = errors.New("invalid or missing PEM private key")
	ErrCertNotFound    = errors.New("imported certificate not found")
	ErrCertInvalidName = errors.New("certificate name must be 1-63 lowercase alphanumeric characters, hyphens, or underscores")
	ErrCertInUse       = errors.New("certificate cannot be deleted because it is referenced by a domain")
)
