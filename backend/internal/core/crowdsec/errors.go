package crowdsec

import "errors"

var (
	ErrCrowdSecUnreachable           = errors.New("crowdsec LAPI is unreachable or returned an invalid status")
	ErrCrowdSecInvalidKey            = errors.New("crowdsec LAPI rejected the bouncer API key (403 Forbidden)")
	ErrCrowdSecInvalidIP             = errors.New("invalid IP address or CIDR range")
	ErrCrowdSecNotFound              = errors.New("CrowdSec resource not found")
	ErrCrowdSecUnavailableCapability = errors.New("CrowdSec operation is not available with the registered capabilities")
)
