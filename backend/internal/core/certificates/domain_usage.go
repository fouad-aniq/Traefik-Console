package certificates

import (
	"sort"
	"time"

	"github.com/traefik-console/internal/models"
	"github.com/traefik-console/internal/traefik/status"
)

// DomainSources are the certificate sources a domain can be checked against. A nil source
// could not be read.
type DomainSources struct {
	Routers  routerList // nil: routers unknown
	Uploaded []models.UIResource[models.CertificateConfig]
	ACME     *status.ACMEInventory
	// UploadedKnown is false when the uploaded certificates could not be read.
	UploadedKnown bool
}

// AnnotateDomain fills Usage and CertificateState of one domain. Certificates are matched the same
// way the Certificates page matches them (ACMEUsage), so both pages always agree.
func AnnotateDomain(d *models.UIResource[models.DomainConfig], src DomainSources) {
	certs := domainCertificates(*d, src)
	d.Config.CertificateState = certificateState(d.Config, certs, src)
	if src.Routers == nil {
		d.Config.Usage = nil
		return
	}
	u := &models.DomainUsage{Routers: []models.RouterRef{}, Certificates: certs}
	for _, r := range src.Routers {
		if r.Config.DomainName == d.Name {
			u.Routers = append(u.Routers, routerRef(r))
		}
	}
	sort.Slice(u.Routers, func(i, j int) bool { return u.Routers[i].Name < u.Routers[j].Name })
	d.Config.Usage = u
}

func domainCertificates(d models.UIResource[models.DomainConfig], src DomainSources) []models.CertificateRef {
	out := []models.CertificateRef{}
	hosts := append([]string{d.Config.Main}, d.Config.SANs...)
	switch d.Config.Method {
	case "files":
		for _, c := range src.Uploaded {
			if c.Name != d.Config.Certificate {
				continue
			}
			covers := true
			for _, h := range hosts {
				covers = covers && Covers(c.Config.Domains, h)
			}
			out = append(out, models.CertificateRef{Source: "uploaded", ID: c.Name, Name: c.Name, Names: nonNil(c.Config.Domains), Issuer: c.Config.Issuer, NotAfter: c.Config.NotAfter, Validity: validity(c.Config.Status), Covers: covers})
		}
	case "resolver":
		if src.ACME == nil {
			return out
		}
		one := []models.UIResource[models.DomainConfig]{d}
		for _, c := range src.ACME.Certificates {
			if c.ResolverName != d.Config.Resolver {
				continue
			}
			names := append([]string{c.MainDomain}, c.SANs...)
			if len(ACMEUsage(names, c.ResolverName, one, src.Routers).Domains) == 0 {
				continue
			}
			out = append(out, models.CertificateRef{Source: "acme", ID: c.Fingerprint, Name: c.MainDomain, Names: names, Issuer: c.Issuer, NotAfter: c.ExpiresAt.UTC().Format(time.RFC3339), Validity: validity(c.Status), Covers: true})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// severity orders validities from best to worst.
var severity = map[string]int{"valid": 0, "not_yet_valid": 1, "expiring": 2, "expired": 3}

// certificateState is one state per domain for the table: the worst validity of its certificates,
// or why there is none.
func certificateState(d models.DomainConfig, certs []models.CertificateRef, src DomainSources) string {
	if d.Method == "files" {
		switch {
		case !src.UploadedKnown:
			return models.CertStateUnavailable
		case len(certs) == 0:
			return models.CertStateMissing
		case !certs[0].Covers:
			return models.CertStateNotCovered
		}
		return certs[0].Validity
	}
	if len(certs) > 0 {
		worst := certs[0].Validity
		for _, c := range certs[1:] {
			if severity[c.Validity] > severity[worst] {
				worst = c.Validity
			}
		}
		return worst
	}
	// No certificate yet: only a fully read storage of this resolver supports that conclusion.
	if src.ACME != nil {
		for _, o := range src.ACME.Observations {
			if o.Resolver == d.Resolver && (o.State == "observed" || o.State == "empty" || o.State == "uninitialized") {
				return models.CertStateNotObserved
			}
		}
	}
	return models.CertStateUnavailable
}

func validity(status string) string {
	switch status {
	case "expiring", "expiring_soon":
		return "expiring"
	case "expired", "not_yet_valid":
		return status
	}
	return "valid"
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
