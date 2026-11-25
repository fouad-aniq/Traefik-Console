package docker

import (
	"context"
	"path/filepath"
)

// ProbeTraefikSecretFile checks readability as the configured container user. The path
// is derived from an operator-registered root and a bounded opaque handle.
func (m *Client) ProbeTraefikSecretFile(ctx context.Context, name string) error {
	if e := ValidateSecretName(name); e != nil {
		return e
	}

	release, e := m.lockOperation(ctx)
	if e != nil {
		return e
	}
	defer release()

	target, e := m.inspectContainer(ctx, m.traefikRef)
	if e != nil || !target.Running {
		return ErrContainerNotRunning
	}

	_, e = m.execFixed(ctx, target.ID, []string{"/usr/bin/test", "-r", filepath.Join(m.secretRoot, name+".secret")})
	return e
}
