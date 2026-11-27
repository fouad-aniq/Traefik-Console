package crowdsec

import (
	"errors"
	"fmt"
)

var (
	ErrCrowdSecUnreachable           = errors.New("crowdsec LAPI is unreachable or returned an invalid status")
	ErrCrowdSecInvalidKey            = errors.New("crowdsec LAPI rejected the bouncer API key (403 Forbidden)")
	ErrCrowdSecInvalidIP             = errors.New("invalid IP address or CIDR range")
	ErrCrowdSecNotFound              = errors.New("CrowdSec resource not found")
	ErrCrowdSecUnavailableCapability = errors.New("CrowdSec operation is not available with the registered capabilities")
	// ErrCrowdSecBlocklistTooLarge: alerts of the community blocklist or of subscribed lists each carry every
	// decision they pulled (thousands), so listing them exceeds what Console reads from the LAPI.
	ErrCrowdSecBlocklistTooLarge = errors.New("blocklist alerts are too large to list; their bans are in the decisions view")
)

// errLAPIResponseTooLarge is a LAPI answer above maxLAPIResponseBytes. It is still an unreachable-LAPI
// error for every caller that does not look for it.
var errLAPIResponseTooLarge = fmt.Errorf("%w: response larger than %d bytes", ErrCrowdSecUnreachable, maxLAPIResponseBytes)
