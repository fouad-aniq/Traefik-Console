package models

// The usage types below are computed at read time by the API (never stored). They carry only
// what the "Used by" dialogs show, so a page does not need the full lists of other resources.

// CertificateUsage is what references a certificate.
type CertificateUsage struct {
	Domains []DomainRef `json:"domains"`
	Routers []RouterRef `json:"routers"`
}

// DomainUsage is what a domain is related to: the routers that use it and the certificates
// that serve its names (the uploaded one it selects, or those its resolver obtained).
type DomainUsage struct {
	Routers      []RouterRef      `json:"routers"`
	Certificates []CertificateRef `json:"certificates"`
}

// MiddlewareUsage is what references a middleware or a chain: routers and other chains.
type MiddlewareUsage struct {
	Routers []RouterRef `json:"routers"`
	Chains  []ChainRef  `json:"chains"`
}

// ServiceUsage is what references a service: the routers that send traffic to it.
type ServiceUsage struct {
	Routers []RouterRef `json:"routers"`
}

// ChainRef identifies a middleware chain.
type ChainRef struct {
	Name     string   `json:"name"`
	Members  []string `json:"members"`
	ReadOnly bool     `json:"read_only"`
}

// DomainRef identifies a domain.
type DomainRef struct {
	Name     string   `json:"name"`
	Main     string   `json:"main"`
	SANs     []string `json:"sans,omitempty"`
	Source   string   `json:"source,omitempty"` // resolver name or uploaded certificate name
	ReadOnly bool     `json:"read_only"`
}

// RouterRef identifies a router.
type RouterRef struct {
	Name        string   `json:"name"`
	Rule        string   `json:"rule,omitempty"`
	EntryPoints []string `json:"entry_points,omitempty"`
	Service     string   `json:"service,omitempty"`
	TLS         bool     `json:"tls"`
	ReadOnly    bool     `json:"read_only"`
}

// RouterRefOf describes a router for a usage list.
func RouterRefOf(r UIResource[RouterConfig]) RouterRef {
	return RouterRef{Name: r.Name, Rule: r.Config.Rule, EntryPoints: r.Config.EntryPoints, Service: r.Config.Service, TLS: r.Config.TLS != nil, ReadOnly: r.ReadOnly}
}

// CertificateRef identifies a certificate: an uploaded one (key = its name) or one Traefik
// obtained through ACME (ID = its fingerprint, as on the Certificates page). The field is not
// called "key": the API response redactor masks every field of that name.
type CertificateRef struct {
	Source   string   `json:"source"` // "uploaded" or "acme"
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Names    []string `json:"names"`
	Issuer   string   `json:"issuer,omitempty"`
	NotAfter string   `json:"not_after,omitempty"`
	Validity string   `json:"validity"` // valid | expiring | expired | not_yet_valid
	// Covers is false when the certificate does not cover every name of the domain.
	Covers bool `json:"covers"`
}

// Certificate states of a domain (DomainConfig.CertificateState).
const (
	CertStateMissing     = "missingCertificate" // the selected uploaded certificate does not exist
	CertStateNotCovered  = "notCovered"         // it exists but does not cover every name
	CertStateNotObserved = "notObserved"        // the resolver has not issued one yet
	CertStateUnavailable = "unavailable"        // the certificate source could not be read
)
