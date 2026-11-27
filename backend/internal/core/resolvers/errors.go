package resolvers

import "errors"

var (
	ErrResolverInvalidName = errors.New("resolver name must be 1-63 lowercase alphanumeric characters, hyphens, or underscores")
	ErrResolverInUse       = errors.New("resolver cannot be deleted because it is referenced by one or more routers")
	ErrEnvChangeForbidden  = errors.New("a custom certificate authority shares one storage file: create a new resolver to use another custom CA address")
)
