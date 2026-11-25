package validation

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"strings"
)

func ValidateCertificate(certPEM, keyPEM, purpose string) ([]*x509.Certificate, error) {
	if len(certPEM) == 0 || len(certPEM) > 1024*1024 || len(keyPEM) > 1024*1024 {
		return nil, fmt.Errorf("certificate material exceeds bounds")
	}
	data := bytes.TrimSpace([]byte(certPEM))
	certs := []*x509.Certificate{}
	for len(data) > 0 {
		block, rest := pem.Decode(data)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) > 0 {
			return nil, fmt.Errorf("invalid certificate PEM")
		}
		c, e := x509.ParseCertificate(block.Bytes)
		if e != nil {
			return nil, fmt.Errorf("invalid certificate")
		}
		certs = append(certs, c)
		if len(certs) > 16 {
			return nil, fmt.Errorf("chain exceeds bounds")
		}
		data = bytes.TrimSpace(rest)
	}
	if len(certs) == 0 {
		return nil, fmt.Errorf("empty certificate chain")
	}
	if purpose == "server" {
		for i := 0; i+1 < len(certs); i++ {
			if e := certs[i].CheckSignatureFrom(certs[i+1]); e != nil {
				return nil, fmt.Errorf("certificate chain is unordered or has invalid signature")
			}
		}
	}
	switch purpose {
	case "server":
		if certs[0].IsCA {
			return nil, fmt.Errorf("CA certificates cannot be server bindings")
		}
		if _, e := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM)); e != nil {
			return nil, fmt.Errorf("certificate and key do not match")
		}
		block, rest := pem.Decode([]byte(keyPEM))
		if block == nil || len(bytes.TrimSpace(rest)) != 0 {
			return nil, fmt.Errorf("expected one private key")
		}
		if len(certs[0].ExtKeyUsage) > 0 {
			ok := false
			for _, u := range certs[0].ExtKeyUsage {
				ok = ok || u == x509.ExtKeyUsageAny || u == x509.ExtKeyUsageServerAuth
			}
			if !ok {
				return nil, fmt.Errorf("certificate cannot authenticate a TLS server")
			}
		}
	case "ca":
		if keyPEM != "" {
			return nil, fmt.Errorf("CA import must not include a private key")
		}
		for _, c := range certs {
			if !c.IsCA || !c.BasicConstraintsValid {
				return nil, fmt.Errorf("invalid CA trust bundle")
			}
		}
	default:
		return nil, fmt.Errorf("invalid certificate purpose")
	}
	return certs, nil
}
func NamesCover(names []string, host string) bool {
	if strings.HasPrefix(host, "*.") {
		for _, n := range names {
			if strings.EqualFold(n, host) {
				return true
			}
		}
		return false
	}
	return (&x509.Certificate{DNSNames: names}).VerifyHostname(host) == nil
}
func CoversDomains(certPEM string, hosts []string) error {
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		return fmt.Errorf("invalid certificate")
	}
	c, e := x509.ParseCertificate(block.Bytes)
	if e != nil {
		return e
	}
	for _, host := range hosts {
		if !NamesCover(c.DNSNames, host) {
			return fmt.Errorf("certificate does not cover a referenced hostname")
		}
	}
	return nil
}
