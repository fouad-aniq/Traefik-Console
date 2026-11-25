package routers

import "errors"

var (
	ErrRouterInvalidName     = errors.New("router name must be 1-63 lowercase alphanumeric characters, hyphens, or underscores")
	ErrRouterRuleEmpty       = errors.New("router must specify at least one domain or a custom rule")
	ErrRouterNoService       = errors.New("router must specify a target service")
	ErrRouterDomainNotFound  = errors.New("selected domain was not found")
	ErrRouterInvalidProtocol = errors.New("router protocol must be http, tcp or udp, and cannot change once created")
	ErrRouterNoBackend       = errors.New("a router without a backend needs a middleware that answers the request, such as a redirect")
)
