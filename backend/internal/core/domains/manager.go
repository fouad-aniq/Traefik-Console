package domains

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/traefik-console/internal/presentation"
	"github.com/traefik-console/internal/traefik/routing"
	"net"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/traefik-console/internal/engine"
	"github.com/traefik-console/internal/models"
	"github.com/traefik-console/internal/traefik/status"
	"github.com/traefik-console/internal/traefik/validation"
)

var domainIdentityPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)

type DomainManager interface {
	ListDomains(ctx context.Context) ([]models.UIResource[models.DomainConfig], error)
	GetDomain(ctx context.Context, name string) (*models.UIResource[models.DomainConfig], error)
	SaveDomain(ctx context.Context, name string, input models.DomainConfig, opts models.WriteOptions) (*models.WriteResult[models.DomainConfig], error)
	DeleteDomain(ctx context.Context, name string, opts models.WriteOptions) (*models.WriteResult[models.DomainConfig], error)
	ProbeDomain(ctx context.Context, name string) (*models.DomainProbeResult, error)
}

func (m *domainManager) ListDomains(ctx context.Context) ([]models.UIResource[models.DomainConfig], error) {
	dbRows, err := m.store.ListItemsByKind(ctx, models.KindDomain)
	if err != nil {
		return nil, err
	}

	// UsedBy is computed at read time from the routers that reference each domain.
	usedBy, err := m.domainUsage(ctx)
	if err != nil {
		return nil, err
	}

	result := make([]models.UIResource[models.DomainConfig], 0)
	for _, row := range dbRows {
		cfg, err := presentation.DecodeUIConfig[models.DomainConfig](row)
		if err != nil {
			return nil, err
		}

		health := "unknown"

		cfg.UsedBy = usedBy[row.Name]

		result = append(result, models.UIResource[models.DomainConfig]{
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

// domainUsage maps each domain name to the routers currently referencing it.
func (m *domainManager) domainUsage(ctx context.Context) (map[string][]string, error) {
	routerRows, err := m.store.ListItemsByKind(ctx, models.KindRouter)
	if err != nil {
		return nil, err
	}

	usedBy := make(map[string][]string)
	for _, rRow := range routerRows {
		var rCfg models.RouterConfig
		if json.Unmarshal(rRow.Payload, &rCfg) == nil {
			for _, domain := range rCfg.DomainNames() {
				usedBy[domain] = append(usedBy[domain], rRow.Name)
			}
		}
	}
	return usedBy, nil
}

func (m *domainManager) GetDomain(ctx context.Context, name string) (*models.UIResource[models.DomainConfig], error) {
	all, err := m.ListDomains(ctx)
	if err != nil {
		return nil, err
	}
	for _, item := range all {
		if item.Name == name {
			return &item, nil
		}
	}
	return nil, ErrDomainNotFound
}

func (m *domainManager) SaveDomain(ctx context.Context, name string, input models.DomainConfig, opts models.WriteOptions) (*models.WriteResult[models.DomainConfig], error) {
	name = strings.TrimSpace(name)
	if !domainIdentityPattern.MatchString(name) {
		return nil, ErrDomainInvalidIdentity
	}

	mainNorm, sansNorm, err := routing.NormalizeSANs(input.Main, input.SANs)
	if err != nil {
		return nil, ErrDomainInvalidIdentity
	}
	input.Name = name
	input.Main = mainNorm
	input.SANs = sansNorm

	if input.Method != "resolver" && input.Method != "files" {
		return nil, ErrDomainMethodRequired
	}
	if input.Method == "resolver" && input.Resolver == "" {
		return nil, ErrDomainResolverNotFound
	}
	if input.Method == "files" && input.Certificate == "" {
		return nil, ErrDomainCertNotFound
	}

	// The referenced resolver or imported certificate must actually exist.
	if input.Method == "resolver" {
		if err := m.validateResolverBinding(ctx, input.Resolver, input.Main, input.SANs); err != nil {
			return nil, err
		}
	} else {
		certRow, err := m.store.GetItem(ctx, models.KindTLS, input.Certificate)
		if err != nil {
			return nil, err
		}
		if certRow == nil {
			return nil, ErrDomainCertNotFound
		}
		var cert models.CertificateConfig
		if json.Unmarshal(certRow.Payload, &cert) != nil || cert.Purpose != "server" {
			return nil, ErrDomainCertNotFound
		}
		for _, host := range append([]string{input.Main}, input.SANs...) {
			if !validation.NamesCover(cert.Domains, host) {
				return nil, ErrDomainCertNotFound
			}
		}

	}

	if input.EntryPoint == "" {
		input.EntryPoint = "websecure"
	}

	input.State = "configured_unverified"
	// Computed at read time; never stored.
	input.UsedBy, input.Usage, input.CertificateState = nil, nil, ""

	rawJSON, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}

	row := models.ConfigItemRow{ID: "domain:" + name, Kind: models.KindDomain, Name: name, Scope: models.ScopeDynamic, Payload: rawJSON}
	return engine.Write(ctx, m.applier, models.Mutation{Upserts: []models.ConfigItemRow{row}}, opts, func(ctx context.Context) (*models.UIResource[models.DomainConfig], error) {
		return m.GetDomain(ctx, name)
	})
}

func (m *domainManager) DeleteDomain(ctx context.Context, name string, opts models.WriteOptions) (*models.WriteResult[models.DomainConfig], error) {
	existing, err := m.store.GetItem(ctx, models.KindDomain, name)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, ErrDomainNotFound
	}

	// Block deletion if any router references this domain
	routers, err := m.store.ListItemsByKind(ctx, models.KindRouter)
	if err != nil {
		return nil, err
	}
	for _, rRow := range routers {
		var rCfg models.RouterConfig
		if json.Unmarshal(rRow.Payload, &rCfg) == nil && rCfg.UsesDomain(name) {
			return nil, fmt.Errorf("%w: used by router '%s'", ErrDomainInUse, rRow.Name)
		}
	}

	return engine.Write[models.DomainConfig](ctx, m.applier, models.Mutation{Deletes: []models.ItemRef{{Kind: models.KindDomain, Name: name}}}, opts, nil)
}

// validateResolverBinding ensures the referenced ACME resolver exists (managed
// in SQLite or discovered in traefik.yml) and that wildcard domains are only
// bound to DNS-01 challenge resolvers. External resolvers are existence-checked
// only: their challenge type is not reliably knowable from outside.
func (m *domainManager) validateResolverBinding(ctx context.Context, resolverName, main string, sans []string) error {
	resRow, err := m.store.GetItem(ctx, models.KindResolver, resolverName)
	if err != nil {
		return err
	}
	if resRow != nil {
		var resCfg models.ResolverInput
		if err := json.Unmarshal(resRow.Payload, &resCfg); err != nil {
			return err
		}
		hasWildcard := strings.HasPrefix(main, "*.")
		for _, s := range sans {
			if strings.HasPrefix(s, "*.") {
				hasWildcard = true
			}
		}
		if hasWildcard && resCfg.Challenge != "dns01" {
			return ErrDomainWildcardNeedsDNS01
		}
		return nil
	}

	if m.files == nil {
		return ErrDomainResolverNotFound
	}
	rawStatic, err := m.files.ReadStaticConfig()
	if err != nil {
		return err
	}
	extSnap, err := status.DiscoverStatic(rawStatic)
	if err != nil {
		return err
	}
	if _, ok := extSnap.Resolvers[resolverName]; !ok {
		return ErrDomainResolverNotFound
	}
	return nil
}

// ProbeDomain performs a read-only TLS handshake against the registered entrypoint target
// and compares the served X.509 certificate against the expected imported or ACME certificate.
func (m *domainManager) ProbeDomain(ctx context.Context, name string) (*models.DomainProbeResult, error) {
	item, err := m.store.GetItem(ctx, models.KindDomain, name)
	if err != nil || item == nil {
		return nil, ErrDomainNotFound
	}

	var d models.DomainConfig
	if err := json.Unmarshal(item.Payload, &d); err != nil {
		return nil, err
	}

	ep := d.EntryPoint
	if ep == "" {
		ep = "websecure"
	}
	target := m.probeTargets[ep]
	if target == "" {
		return nil, ErrDomainProbeInvalid
	}
	// A domain is a zone; Traefik answers for the hosts its routes request. Probe one of those,
	// otherwise Traefik serves its default certificate and every check reads as a mismatch.
	host, err := m.probeHost(ctx, name, d)
	if err != nil {
		return nil, err
	}
	if _, _, err := net.SplitHostPort(target); err != nil {
		return nil, ErrDomainProbeInvalid
	}

	now := time.Now().UTC()
	res := &models.DomainProbeResult{
		State:      "unreachable",
		Trust:      "unknown",
		Hostname:   host,
		Address:    target,
		ObservedAt: now.Format(time.RFC3339),
	}

	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	conn, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(probeCtx, "tcp", target)
	if err != nil {
		res.Error = "Nothing answered at " + target + " for the " + ep + " entry point. Publish Traefik's " + ep + " port to the Console and set TRAEFIK_PROBE_TARGETS (" + ep + "=host:port)."
		return res, nil
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	tlsClient := tls.Client(conn, &tls.Config{
		ServerName:         host,
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true, // Trust is verified explicitly below so staging/self-signed certs can still be inspected
	})
	if err := tlsClient.HandshakeContext(probeCtx); err != nil {
		res.Error = "TLS handshake failed"
		return res, nil
	}

	state := tlsClient.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		res.Error = "Peer did not serve a certificate"
		return res, nil
	}

	leaf := state.PeerCertificates[0]
	hash := sha256.Sum256(leaf.Raw)
	res.Fingerprint = hex.EncodeToString(hash[:])
	res.Issuer = leaf.Issuer.String()
	res.Names = strings.Join(leaf.DNSNames, ", ")
	res.NotBefore = leaf.NotBefore.UTC().Format(time.RFC3339)
	res.NotAfter = leaf.NotAfter.UTC().Format(time.RFC3339)
	res.Algorithm = leaf.PublicKeyAlgorithm.String()

	if now.Before(leaf.NotBefore) {
		res.Validity = "not_yet_valid"
	} else if !now.Before(leaf.NotAfter) {
		res.Validity = "expired"
	} else if leaf.NotAfter.Sub(now) <= 30*24*time.Hour {
		res.Validity = "expiring"
	} else {
		res.Validity = "valid"
	}

	res.State = "observed"
	if err := leaf.VerifyHostname(host); err != nil {
		res.State = "mismatch"
	}

	inter := x509.NewCertPool()
	for _, c := range state.PeerCertificates[1:] {
		inter.AddCert(c)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{DNSName: host, Intermediates: inter, CurrentTime: now}); err == nil {
		res.Trust = "trusted"
	} else {
		res.Trust = "untrusted"
	}

	// Compare fingerprint against expected Imported Certificate or ACME Certificate
	if d.Method == "files" && d.Certificate != "" {
		if certRow, err := m.store.GetItem(ctx, models.KindTLS, d.Certificate); err == nil && certRow != nil {
			var certCfg models.CertificateConfig
			if json.Unmarshal(certRow.Payload, &certCfg) == nil {
				res.Expected = certCfg.Fingerprint
				if res.State != "mismatch" && res.Fingerprint == certCfg.Fingerprint {
					res.State = "matches"
				} else {
					res.State = "mismatch"
				}
			}
		}
	} else if d.Method == "resolver" && res.State != "mismatch" {
		raw, e := m.files.ReadStaticConfig()
		if e == nil && m.acmeObserver != nil {
			inventory := m.acmeObserver.Observe(raw)
			if cert := inventory.Find(d.Resolver, []string{host}); cert != nil {
				res.Expected = cert.Fingerprint
				if res.Fingerprint == cert.Fingerprint {
					res.State = "matches"
				} else {
					res.State = "mismatch"
				}
			}
		}

	}

	return res, nil
}

// probeHost picks the hostname to probe: the domain's main name when one of its routers requests
// it, otherwise the first concrete hostname its routers request.
func (m *domainManager) probeHost(ctx context.Context, name string, d models.DomainConfig) (string, error) {
	rows, err := m.store.ListItemsByKind(ctx, models.KindRouter)
	if err != nil {
		return "", err
	}
	var hosts []string
	for _, row := range rows {
		var r models.RouterConfig
		if json.Unmarshal(row.Payload, &r) != nil || !r.UsesDomain(name) {
			continue
		}
		// A router with hostnames on several domains: only the ones inside this domain.
		inZone := func(h string) bool {
			h = strings.ToLower(strings.TrimSpace(h))
			for _, zone := range append([]string{d.Main}, d.SANs...) {
				z := strings.TrimPrefix(strings.ToLower(zone), "*.")
				if h == z || h == "*."+z || strings.HasSuffix(h, "."+z) {
					return true
				}
			}
			return false
		}
		for _, h := range r.Domains {
			if inZone(h) {
				hosts = append(hosts, h)
			}
		}
		if r.TLS != nil {
			for _, td := range r.TLS.Domains {
				for _, h := range append([]string{td.Main}, td.SANs...) {
					if inZone(h) {
						hosts = append(hosts, h)
					}
				}
			}
		}
	}
	concrete := []string{}
	for _, h := range hosts {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" || strings.HasPrefix(h, "*.") {
			continue
		}
		if h == strings.ToLower(d.Main) {
			return h, nil
		}
		concrete = append(concrete, h)
	}
	if len(concrete) == 0 {
		return "", ErrDomainProbeInvalid
	}
	sort.Strings(concrete)
	return concrete[0], nil
}
