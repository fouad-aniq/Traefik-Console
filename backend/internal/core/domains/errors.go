package domains

import "errors"

var (
	ErrDomainInvalidIdentity    = errors.New("domain identifier must be 1-63 lowercase alphanumeric characters, hyphens, or underscores")
	ErrDomainNotFound           = errors.New("domain configuration not found")
	ErrDomainMethodRequired     = errors.New("choose an existing ACME resolver or an imported certificate")
	ErrDomainProbeInvalid       = errors.New("domain cannot be probed: requires a router on the domain with a concrete hostname, and a registered entrypoint target")
	ErrDomainInUse              = errors.New("domain cannot be deleted because it is referenced by one or more routers")
	ErrDomainResolverNotFound   = errors.New("selected ACME resolver does not exist")
	ErrDomainCertNotFound       = errors.New("selected imported certificate does not exist")
	ErrDomainWildcardNeedsDNS01 = errors.New("wildcard domains (*.example.com) require a DNS-01 challenge resolver")
)
