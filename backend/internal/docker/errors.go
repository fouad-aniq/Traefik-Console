package docker

import "errors"

var (
	ErrOperationBusy       = errors.New("container operation already in progress")
	ErrContainerNotFound   = errors.New("target container not found or access denied")
	ErrContainerNotRunning = errors.New("target container is not in running state")
	ErrRestartUnconfirmed  = errors.New("traefik restart failed or new start timestamp was not observed")
	ErrExecFailed          = errors.New("container exec command failed or exceeded 64 KiB limit")
	ErrRestartDisabled     = errors.New("container restart capability is disabled by configuration")
)

var (
	ErrCapabilityDisabled = errors.New("Docker capability is disabled")
	ErrUnavailable        = errors.New("registered Docker endpoint is unavailable")
	ErrInvalidRequest     = errors.New("invalid container operation input")
)
