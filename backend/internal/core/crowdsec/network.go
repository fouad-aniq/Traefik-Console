package crowdsec

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/traefik-console/internal/config/secrets"
)

const maxIntegrationPEMBytes = 1 << 20

func registeredURL(raw string) (*url.URL, error) {
	u, e := url.Parse(strings.TrimRight(strings.TrimSpace(raw), "/"))
	if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return nil, ErrCrowdSecUnreachable
	}
	return u, nil
}

// Addresses are resolved once per verification operation and pinned for its dial.
// No proxy or redirects can move a credential to a different authority.
func pinnedClient(ctx context.Context, u *url.URL) (*http.Client, error) {
	return pinnedClientWithTLS(ctx, u, nil, 4*time.Second)
}

func pinnedClientWithTLS(ctx context.Context, u *url.URL, tlsConfig *tls.Config, timeout time.Duration) (*http.Client, error) {
	ips, e := net.DefaultResolver.LookupIPAddr(ctx, u.Hostname())
	if e != nil || len(ips) == 0 {
		return nil, ErrCrowdSecUnreachable
	}
	for _, v := range ips {
		if v.IP.IsLinkLocalUnicast() || v.IP.IsLinkLocalMulticast() || v.IP.IsUnspecified() || v.IP.IsMulticast() {
			return nil, ErrCrowdSecUnreachable
		}
	}
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	if timeout <= 0 || timeout > 120*time.Second {
		timeout = 20 * time.Second
	}
	tr := &http.Transport{Proxy: nil, TLSClientConfig: tlsConfig, TLSHandshakeTimeout: 2 * time.Second, ResponseHeaderTimeout: timeout, MaxResponseHeaderBytes: 8192, MaxConnsPerHost: 2, MaxIdleConnsPerHost: 2, IdleConnTimeout: 30 * time.Second, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != net.JoinHostPort(u.Hostname(), port) {
			return nil, fmt.Errorf("unregistered LAPI destination")
		}
		return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}}
	return &http.Client{Timeout: timeout, Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}

func readIntegrationPEM(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxIntegrationPEMBytes {
		return nil, ErrCrowdSecUnreachable
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, ErrCrowdSecUnreachable
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxIntegrationPEMBytes+1))
	if err != nil || len(data) == 0 || len(data) > maxIntegrationPEMBytes {
		return nil, ErrCrowdSecUnreachable
	}
	return data, nil
}

func integrationTLSConfig(caFile, certFile, keyFile string) (*tls.Config, error) {
	var caPEM, certPEM, keyPEM []byte
	var err error
	if caFile != "" {
		caPEM, err = readIntegrationPEM(caFile)
		if err != nil {
			return nil, err
		}
	}
	if certFile != "" {
		certPEM, err = readIntegrationPEM(certFile)
		if err != nil {
			return nil, err
		}
		keyPEM, err = readIntegrationPEM(keyFile)
		if err != nil {
			return nil, err
		}
	}
	if caFile == "" && certFile == "" {
		return nil, nil
	}
	return tlsConfigFromPEM(caPEM, certPEM, keyPEM)
}

func tlsConfigFromPEM(caPEM, certPEM, keyPEM []byte) (*tls.Config, error) {
	if len(certPEM) == 0 && len(keyPEM) != 0 || len(certPEM) != 0 && len(keyPEM) == 0 {
		return nil, ErrCrowdSecUnreachable
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if len(caPEM) != 0 {
		roots, err := x509.SystemCertPool()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(caPEM) {
			return nil, ErrCrowdSecUnreachable
		}
		cfg.RootCAs = roots
	}
	if len(certPEM) != 0 {
		cert, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			return nil, ErrCrowdSecUnreachable
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg, nil
}

func (m *crowdsecManager) configureTransports() error {
	var err error
	m.bouncerTLS, err = integrationTLSConfig(m.settings.CAFile, m.settings.BouncerCertFile, m.settings.BouncerKeyFile)
	if err != nil {
		return err
	}
	m.machineTLS, err = integrationTLSConfig(m.settings.CAFile, m.settings.MachineCertFile, m.settings.MachineKeyFile)
	return err
}

func (m *crowdsecManager) effectiveLAPIURL(ctx context.Context) (string, error) {
	if candidate := overrideLAPIURL(ctx); candidate != "" {
		return candidate, nil
	}
	cfg, err := m.GetConfig(ctx)
	if err != nil {
		return "", err
	}
	return cfg.LAPIURL, nil
}

func (m *crowdsecManager) effectiveReadTimeout(ctx context.Context) (time.Duration, error) {
	if m.settings.ReadTimeoutSet {
		return m.settings.ReadTimeout, nil
	}
	cfg, err := m.GetConfig(ctx)
	if err != nil {
		return 0, err
	}
	if cfg.ReadTimeoutSeconds >= 1 && cfg.ReadTimeoutSeconds <= 120 {
		return time.Duration(cfg.ReadTimeoutSeconds) * time.Second, nil
	}
	return m.settings.ReadTimeout, nil
}

func (m *crowdsecManager) effectiveAlertLimit(ctx context.Context) (int, error) {
	if m.settings.AlertLimitSet {
		return m.settings.AlertLimit, nil
	}
	cfg, err := m.GetConfig(ctx)
	if err != nil {
		return 0, err
	}
	if cfg.AlertLimit >= 1 && cfg.AlertLimit <= 1000 {
		return cfg.AlertLimit, nil
	}
	return m.settings.AlertLimit, nil
}

func (m *crowdsecManager) secretPEM(ctx context.Context, name string) ([]byte, error) {
	value, err := m.secrets.DecryptSecret(ctx, name)
	if errors.Is(err, secrets.ErrSecretNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return []byte(strings.TrimSpace(value)), nil
}

func (m *crowdsecManager) tlsForRole(ctx context.Context, machine bool) (*tls.Config, bool, error) {
	base := m.bouncerTLS
	envCertificate := m.settings.BouncerCertFile != ""
	if machine {
		base = m.machineTLS
		envCertificate = m.settings.MachineCertFile != ""
	}
	if envCertificate {
		return base, true, nil
	}
	certPEM, err := m.secretPEM(ctx, crowdsecClientCertName)
	if err != nil {
		return nil, false, err
	}
	keyPEM, err := m.secretPEM(ctx, crowdsecClientKeySecret)
	if err != nil {
		return nil, false, err
	}
	if len(certPEM) == 0 && len(keyPEM) == 0 {
		if m.settings.CAFile != "" {
			return base, false, nil
		}
		caPEM, caErr := m.secretPEM(ctx, crowdsecCACertSecret)
		if caErr != nil || len(caPEM) == 0 {
			return base, false, caErr
		}
		cfg, caErr := tlsConfigFromPEM(caPEM, nil, nil)
		return cfg, false, caErr
	}
	var caPEM []byte
	if m.settings.CAFile == "" {
		caPEM, err = m.secretPEM(ctx, crowdsecCACertSecret)
		if err != nil {
			return nil, false, err
		}
	}
	cfg, err := tlsConfigFromPEM(caPEM, certPEM, keyPEM)
	if err != nil {
		return nil, false, err
	}
	if base != nil && m.settings.CAFile != "" {
		cfg.RootCAs = base.RootCAs
	}
	return cfg, true, nil
}

func (m *crowdsecManager) hasClientCertificate(ctx context.Context, machine bool) (bool, error) {
	_, present, err := m.tlsForRole(ctx, machine)
	return present, err
}

func (m *crowdsecManager) clientForURL(ctx context.Context, rawURL string, machine bool) (*http.Client, error) {
	u, err := registeredURL(rawURL)
	if err != nil {
		return nil, err
	}
	tlsConfig, _, err := m.tlsForRole(ctx, machine)
	if err != nil {
		return nil, err
	}
	timeout, err := m.effectiveReadTimeout(ctx)
	if err != nil {
		return nil, err
	}
	return pinnedClientWithTLS(ctx, u, tlsConfig, timeout)
}

func (m *crowdsecManager) clientForRole(ctx context.Context, machine bool) (*http.Client, error) {
	if machine && m.machineClient != nil {
		return m.machineClient, nil
	}
	if !machine && m.httpClient != nil {
		return m.httpClient, nil
	}
	lapiURL, err := m.effectiveLAPIURL(ctx)
	if err != nil {
		return nil, err
	}
	return m.clientForURL(ctx, lapiURL, machine)
}
