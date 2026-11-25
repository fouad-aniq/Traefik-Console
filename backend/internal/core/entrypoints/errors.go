package entrypoints

import "errors"

var (
	ErrEntryPointInvalidName  = errors.New("entrypoint name must be 1-63 lowercase alphanumeric characters, hyphens, or underscores")
	ErrEntryPointInUse        = errors.New("entrypoint cannot be deleted because it is still referenced")
	ErrEntryPointLast         = errors.New("the last entrypoint cannot be deleted; Traefik needs at least one")
	ErrEntryPointProtocol     = errors.New("the protocol of an entrypoint cannot change; create a new entrypoint instead")
	ErrEntryPointNotAdoptable = errors.New("this entrypoint uses settings the console does not manage; edit traefik.yml by hand")
)
