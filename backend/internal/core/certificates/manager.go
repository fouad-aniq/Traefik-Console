package certificates

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/traefik-console/internal/presentation"
	"regexp"
	"strings"
	"time"

	"github.com/traefik-console/internal/engine"
	"github.com/traefik-console/internal/models"
	"github.com/traefik-console/internal/traefik/validation"
)

type CertificateManager interface {
	ListCertificates(ctx context.Context) ([]models.UIResource[models.CertificateConfig], error)
	GetCertificate(ctx context.Context, name string) (*models.UIResource[models.CertificateConfig], error)
	ImportCertificate(ctx context.Context, name, purpose, certPEM, keyPEM string, opts models.WriteOptions) (*models.WriteResult[models.CertificateConfig], error)
	DeleteCertificate(ctx context.Context, name string, opts models.WriteOptions) (*models.WriteResult[models.CertificateConfig], error)
}

var resolverNameRegex = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)

func (m *certificateManager) ListCertificates(ctx context.Context) ([]models.UIResource[models.CertificateConfig], error) {
	dbRows, err := m.store.ListItemsByKind(ctx, models.KindTLS)
	if err != nil {
		return nil, err
	}

	result := make([]models.UIResource[models.CertificateConfig], 0)
	for _, row := range dbRows {
		cfg, err := presentation.DecodeUIConfig[models.CertificateConfig](row)
		if err != nil {
			return nil, err
		}

		// Validity changes with time, even if the stored certificate is unchanged.
		if expires, e := time.Parse(time.RFC3339, cfg.NotAfter); e == nil {
			now := time.Now().UTC()
			start, _ := time.Parse(time.RFC3339, cfg.NotBefore)
			switch {
			case now.Before(start):
				cfg.Status = "not_yet_valid"
			case !now.Before(expires):
				cfg.Status = "expired"
			case expires.Sub(now) <= 30*24*time.Hour:
				cfg.Status = "expiring"
			default:
				cfg.Status = "valid"
			}
		}
		health := "unknown"

		result = append(result, models.UIResource[models.CertificateConfig]{
			ID:         row.ID,
			UpdatedAt:  presentation.ParseTimestamp(row.UpdatedAt),
			Name:       row.Name,
			Origin:     "managed",
			Provider:   "file",
			Status:     models.StatusActive,
			ReadOnly:   false,
			LiveHealth: health,
			Config:     cfg,
			Version:    row.Version,
		})
	}
	return result, nil
}

func (m *certificateManager) GetCertificate(ctx context.Context, name string) (*models.UIResource[models.CertificateConfig], error) {
	all, err := m.ListCertificates(ctx)
	if err != nil {
		return nil, err
	}

	for _, item := range all {
		if item.Name == name {
			return &item, nil
		}
	}
	return nil, ErrCertNotFound
}

func (m *certificateManager) ImportCertificate(ctx context.Context, name, purpose, certPEM, keyPEM string, opts models.WriteOptions) (*models.WriteResult[models.CertificateConfig], error) {
	name = strings.TrimSpace(name)
	if !resolverNameRegex.MatchString(name) {
		return nil, ErrCertInvalidName
	}

	if purpose == "" {
		purpose = "server"
	}
	if purpose != "server" && purpose != "ca" {
		return nil, ErrCertInvalidPEM
	}

	parsedCerts, err := validation.ValidateCertificate(certPEM, keyPEM, purpose)
	if err != nil {
		return nil, ErrCertInvalidPEM
	}
	// Check every referencing domain before replacing material.
	domains, err := m.store.ListItemsByKind(ctx, models.KindDomain)
	if err != nil {
		return nil, err
	}
	for _, d := range domains {
		var cfg models.DomainConfig
		if json.Unmarshal(d.Payload, &cfg) != nil {
			return nil, ErrCertInvalidPEM
		}
		if cfg.Method == "files" && cfg.Certificate == name {
			if purpose != "server" || validation.CoversDomains(certPEM, append([]string{cfg.Main}, cfg.SANs...)) != nil {
				return nil, ErrCertInUse
			}
		}
	}
	mutation := models.Mutation{}
	// The public certificate chain file
	certSec, err := m.secrets.PrepareSecret(ctx, "tls_cert_"+name, "tls_cert", "", strings.TrimSpace(certPEM))
	if err != nil {
		return nil, err
	}

	mutation.Secrets = append(mutation.Secrets, *certSec)
	var keyFilePath string
	if purpose == "server" {
		keySec, err := m.secrets.PrepareSecret(ctx, "tls_key_"+name, "tls_key", "", strings.TrimSpace(keyPEM))
		if err != nil {
			return nil, err
		}
		keyFilePath = keySec.FilePath
		mutation.Secrets = append(mutation.Secrets, *keySec)
	}

	leaf := parsedCerts[0]
	hash := sha256.Sum256(leaf.Raw)
	now := time.Now().UTC()

	status := "valid"
	if now.Before(leaf.NotBefore) {
		status = "not_yet_valid"
	} else if !now.Before(leaf.NotAfter) {
		status = "expired"
	} else if leaf.NotAfter.Sub(now) <= 30*24*time.Hour {
		status = "expiring"
	}

	certData := models.CertificateConfig{
		Name:        name,
		Source:      "imported",
		CertFile:    certSec.FilePath,
		KeyFile:     keyFilePath,
		Fingerprint: hex.EncodeToString(hash[:]),
		Domains:     append([]string{}, leaf.DNSNames...),
		Issuer:      leaf.Issuer.String(),
		NotBefore:   leaf.NotBefore.UTC().Format(time.RFC3339),
		NotAfter:    leaf.NotAfter.UTC().Format(time.RFC3339),
		Algorithm:   leaf.PublicKeyAlgorithm.String(),
		Status:      status,
		Purpose:     purpose,
	}

	rawJSON, err := json.Marshal(certData)
	if err != nil {
		return nil, err
	}

	mutation.Upserts = []models.ConfigItemRow{{ID: "tls:certificate:" + name, Kind: models.KindTLS, Name: name, Scope: models.ScopeDynamic, Payload: rawJSON}}
	return engine.Write(ctx, m.applier, mutation, opts, func(ctx context.Context) (*models.UIResource[models.CertificateConfig], error) {
		return m.GetCertificate(ctx, name)
	})
}

func (m *certificateManager) DeleteCertificate(ctx context.Context, name string, opts models.WriteOptions) (*models.WriteResult[models.CertificateConfig], error) {
	existing, err := m.store.GetItem(ctx, models.KindTLS, name)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, ErrCertNotFound
	}

	// Block deletion if any Domain references this imported certificate
	domains, err := m.store.ListItemsByKind(ctx, models.KindDomain)
	if err != nil {
		return nil, err
	}
	for _, dRow := range domains {
		var dCfg models.DomainConfig
		if json.Unmarshal(dRow.Payload, &dCfg) == nil {
			if dCfg.Method == "files" && dCfg.Certificate == name {
				return nil, fmt.Errorf("%w: used by domain '%s'", ErrCertInUse, dRow.Name)
			}
		}
	}

	// The certificate and its key live in secrets the console owns: delete them with it.
	mutation := models.Mutation{Deletes: []models.ItemRef{{Kind: models.KindTLS, Name: name}}, SecretDeletes: []string{"tls_cert_" + name, "tls_key_" + name}}
	return engine.Write[models.CertificateConfig](ctx, m.applier, mutation, opts, nil)
}
