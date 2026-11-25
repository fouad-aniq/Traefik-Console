package status

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"time"

	"github.com/traefik-console/internal/traefik/validation"
)

const maxAcmeFileSize = 16 * 1024 * 1024 // 16 MiB cap for acme.json

// acmeResolverBlock matches Traefik's internal acme.json structure per resolver:
// { "letsencrypt": { "Account": {...}, "Certificates": [ { "domain": {...}, "certificate": "base64..." } ] } }
type acmeResolverBlock struct {
	Certificates []struct {
		Domain struct {
			Main string   `json:"main"`
			SANs []string `json:"sans"`
		} `json:"domain"`
		Certificate string `json:"certificate"` // Base64-encoded PEM bundle
		// Private key/account fields are never decoded into exported metadata.
	} `json:"Certificates"`
}

func parseBase64Cert(resolver, mainDomain string, sans []string, b64Cert string, now time.Time) (CertificateInfo, error) {
	pemBytes, err := base64.StdEncoding.DecodeString(b64Cert)
	if err != nil {
		return CertificateInfo{}, err
	}

	// Decode the first PEM block (the leaf certificate)
	block, _ := pem.Decode(pemBytes)
	if block == nil || block.Type != "CERTIFICATE" {
		return CertificateInfo{}, errors.New("no valid PEM certificate block found")
	}

	x509Cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return CertificateInfo{}, err
	}

	hash := sha256.Sum256(x509Cert.Raw)
	fingerprint := hex.EncodeToString(hash[:])

	if mainDomain == "" {
		if len(x509Cert.DNSNames) > 0 {
			mainDomain = x509Cert.DNSNames[0]
		} else {
			mainDomain = x509Cert.Subject.CommonName
		}
	} else if !validation.NamesCover(x509Cert.DNSNames, mainDomain) {
		return CertificateInfo{}, errors.New("ACME domain metadata does not match certificate")
	}
	for _, name := range sans {
		if !validation.NamesCover(x509Cert.DNSNames, name) {
			return CertificateInfo{}, errors.New("ACME SAN metadata does not match certificate")
		}
	}
	sans = nil
	if len(sans) == 0 && len(x509Cert.DNSNames) > 0 {
		for _, dns := range x509Cert.DNSNames {
			if dns != mainDomain {
				sans = append(sans, dns)
			}
		}
	}

	expiresAt := x509Cert.NotAfter.UTC()
	daysLeft := int(expiresAt.Sub(now).Hours() / 24)

	status := "valid"
	if now.Before(x509Cert.NotBefore) {
		status = "not_yet_valid"
	} else if !now.Before(expiresAt) {
		status = "expired"
		daysLeft = 0
	} else if daysLeft <= 30 {
		status = "expiring_soon"
	}

	issuer := x509Cert.Issuer.CommonName
	if issuer == "" && len(x509Cert.Issuer.Organization) > 0 {
		issuer = x509Cert.Issuer.Organization[0]
	}

	return CertificateInfo{
		ResolverName: resolver,
		MainDomain:   mainDomain,
		SANs:         sans,
		Issuer:       issuer,
		SerialNumber: x509Cert.SerialNumber.String(),
		Fingerprint:  fingerprint,
		NotBefore:    x509Cert.NotBefore.UTC(),
		ExpiresAt:    expiresAt,
		DaysLeft:     daysLeft,
		Status:       status,
	}, nil
}
