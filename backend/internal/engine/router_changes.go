package engine

import (
	"bytes"
	"encoding/json"
	"github.com/traefik-console/internal/models"
	"github.com/traefik-console/internal/traefik/routing"
)

// derivedRouterChanges lists routers whose rendered output changes only because
// something they reference (a domain, a resolver) changes. The router's own
// stored payload is untouched, so it is reported as "impacted", not edited.
func derivedRouterChanges(active, candidate []models.ConfigItemRow, liveStatic, candidateStatic []byte) ([]models.ItemChange, error) {
	live, err := routing.NewView(active, liveStatic)
	if err != nil {
		return nil, err
	}
	effective, err := routing.NewView(candidate, candidateStatic)
	if err != nil {
		return nil, err
	}
	previous := rowsByKey(active)
	var changes []models.ItemChange
	for _, row := range candidate {
		old, existed := previous[itemKey(row.Kind, row.Name)]
		// Routers written by this very mutation are reported as created/updated already.
		if row.Kind != models.KindRouter || !existed || !bytes.Equal(old.Payload, row.Payload) {
			continue
		}
		var input models.RouterConfig
		if err = json.Unmarshal(row.Payload, &input); err != nil {
			return nil, err
		}
		a, activeErr := live.Project(input)
		b, pendingErr := effective.Project(input)
		aj, _ := json.Marshal(a)
		bj, _ := json.Marshal(b)
		if !bytes.Equal(aj, bj) || (activeErr != nil) != (pendingErr != nil) {
			changes = append(changes, models.ItemChange{ID: itemKey(row.Kind, row.Name), Name: row.Name, Kind: models.KindRouter, Scope: models.ScopeDynamic, Action: models.ChangeImpacted})
		}
	}
	return changes, nil
}
