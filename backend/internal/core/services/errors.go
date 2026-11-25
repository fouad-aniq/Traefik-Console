package services

import "errors"

var (
	ErrServiceInvalidName = errors.New("service name must be 1-63 lowercase alphanumeric characters, hyphens, or underscores")
	ErrServiceNoServers   = errors.New("at least one backend server is required")
	ErrServiceInvalidURL  = errors.New("backend server URL must be a valid http:// or https:// URL")
	ErrServiceInUse       = errors.New("service cannot be deleted because it is referenced by one or more routers")

	// ErrServiceInvalidProtocol is returned for a protocol other than http, tcp or udp, and for
	// an edit that changes a service's protocol (Traefik keeps each in a separate section).
	ErrServiceInvalidProtocol = errors.New("service protocol must be http, tcp or udp and cannot change")
	// ErrServiceInvalidAddress is returned for a TCP/UDP backend that is not host:port.
	ErrServiceInvalidAddress = errors.New("backend server address must be host:port with a port between 1 and 65535")
	// ErrServiceInvalidOption is returned for an option the protocol does not have or a bad value.
	ErrServiceInvalidOption = errors.New("service option is invalid for this protocol")
)
