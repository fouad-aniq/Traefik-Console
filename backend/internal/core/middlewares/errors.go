package middlewares

import "errors"

var (
	ErrMiddlewareInvalidName     = errors.New("middleware name must be 1-63 lowercase alphanumeric characters, hyphens, or underscores")
	ErrMiddlewareInvalidType     = errors.New("unsupported or empty middleware type")
	ErrChainEmpty                = errors.New("middleware chain must contain at least one middleware")
	ErrChainSelfReference        = errors.New("middleware chain cannot reference itself")
	ErrMiddlewareInvalidProtocol = errors.New("middleware protocol must be http or tcp, and cannot change once created")
	ErrMiddlewareInUse           = errors.New("middleware cannot be deleted because it is referenced by a router or chain")
)
