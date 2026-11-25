package engine

import (
	"context"

	"github.com/traefik-console/internal/config/files"
	"github.com/traefik-console/internal/models"
)

// ConfigReader observes registered configuration without publication privileges.
type ConfigReader interface {
	ReadStaticConfig() ([]byte, error)
	ReadManagedDynamicConfig() ([]byte, error)
	ListExternalDynamicFiles() ([]files.ExternalFile, error)
	ReadExternalDynamicFile(string) ([]byte, error)
}

// ConfigFiles belongs to operation execution/recovery, never resource endpoints.
type ConfigFiles interface {
	ConfigReader
	WriteStaticConfig([]byte) error
	WriteManagedDynamicConfig([]byte) error
	RestoreStaticConfig([]byte) error
	RestoreManagedDynamicConfig([]byte) error
	ReadConsoleDomainsConfig() ([]byte, error)
	WriteConsoleDomainsConfig([]byte) error
	RestoreConsoleDomainsConfig([]byte) error
}

type SecretRuntime interface {
	SameValue([]byte, []byte) (bool, error)
	Decrypt([]byte) (string, error)
	DecryptSecret(context.Context, string) (string, error)
	CaptureRuntime(context.Context, []string) (map[string][]byte, error)
	CandidateRuntime([]models.SecretRow) map[string][]byte
	RestoreRuntime(context.Context, map[string][]byte, map[string][]byte) error
	FlushSecretsToDisk(context.Context, []models.SecretRow, []string) error
}
