package docker

import (
	"context"
	"encoding/json"
	"strconv"
)

func machineName(id int64) string {
	return "traefik-console-machine-" + strconv.FormatInt(id, 10)
}

func (m *Client) RegisterMachine(ctx context.Context, id int64, password string) (string, error) {
	if e := ValidateBouncerInput(id, password, true); e != nil {
		return "", ErrInvalidRequest
	}
	release, e := m.lockOperation(ctx)
	if e != nil {
		return "", e
	}
	defer release()
	name := machineName(id)
	// The Console generated the password, so the YAML emitted on stdout is not
	// needed and is deliberately discarded by the bounded executor.
	_, e = m.execCrowdSec(ctx, "machines", "add", name, "--password", password, "-f-")
	return name, e
}

func (m *Client) RevokeMachine(ctx context.Context, id int64) error {
	if e := ValidateBouncerInput(id, "", false); e != nil {
		return e
	}
	release, e := m.lockOperation(ctx)
	if e != nil {
		return e
	}
	defer release()
	raw, e := m.execCrowdSec(ctx, "machines", "list", "-o", "json")
	if e != nil {
		return e
	}
	// cscli names a machine "machineId" in its JSON (bouncers use "name").
	var list []struct {
		MachineID string `json:"machineId"`
	}
	if json.Unmarshal(raw, &list) != nil {
		return ErrExecFailed
	}
	name := machineName(id)
	for _, entry := range list {
		if entry.MachineID == name {
			_, e = m.execCrowdSec(ctx, "machines", "delete", name)
			return e
		}
	}
	return nil
}
