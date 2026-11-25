package engine

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/traefik-console/internal/config/files"
	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/models"
)

const (
	maxCandidateItems = 1000
	maxCandidateBytes = 8 * 1024 * 1024
)

func itemKey(kind models.ItemKind, name string) string { return string(kind) + ":" + name }

func rowsByKey(rows []models.ConfigItemRow) map[string]models.ConfigItemRow {
	out := make(map[string]models.ConfigItemRow, len(rows))
	for _, row := range rows {
		out[itemKey(row.Kind, row.Name)] = row
	}
	return out
}

// buildCandidate merges one mutation into the committed state, in memory. The
// returned state is what the database will hold once the operation commits.
func buildCandidate(active *db.State, m models.Mutation) (*db.State, error) {
	items := rowsByKey(active.Items)
	for _, ref := range m.Deletes {
		if _, ok := items[ref.Key()]; !ok {
			return nil, errNotFound()
		}
		delete(items, ref.Key())
	}
	for _, up := range m.Upserts {
		key := itemKey(up.Kind, up.Name)
		old, exists := items[key]
		row := up
		row.ID = key
		if exists {
			row.Version = old.Version + 1
			row.CreatedAt = old.CreatedAt
		} else {
			row.Version = 1
		}
		items[key] = row
	}
	secrets := map[string]db.SecretState{}
	for _, s := range active.Secrets {
		secrets[s.Metadata.Name] = s
	}
	for _, s := range m.Secrets {
		secrets[s.Name] = db.SecretState{Metadata: s, Active: s.EncryptedValue}
	}
	for _, name := range m.SecretDeletes {
		delete(secrets, name)
	}
	candidate := &db.State{Items: make([]models.ConfigItemRow, 0, len(items)), Secrets: make([]db.SecretState, 0, len(secrets))}
	for _, row := range items {
		candidate.Items = append(candidate.Items, row)
	}
	sort.Slice(candidate.Items, func(i, j int) bool {
		a, b := candidate.Items[i], candidate.Items[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Name < b.Name
	})
	for _, s := range secrets {
		candidate.Secrets = append(candidate.Secrets, s)
	}
	sort.Slice(candidate.Secrets, func(i, j int) bool { return candidate.Secrets[i].Metadata.Name < candidate.Secrets[j].Metadata.Name })
	if len(candidate.Items) > maxCandidateItems {
		return nil, ErrValidationFailed
	}
	encoded, err := json.Marshal(candidate)
	if err != nil {
		return nil, err
	}
	if len(encoded) > maxCandidateBytes {
		return nil, ErrValidationFailed
	}
	return candidate, nil
}

// diffItems lists the resources whose stored payload differs between two sets.
func diffItems(before, after []models.ConfigItemRow) []models.ItemChange {
	old := rowsByKey(before)
	next := rowsByKey(after)
	var changes []models.ItemChange
	for key, row := range next {
		prev, existed := old[key]
		switch {
		case !existed:
			changes = append(changes, change(row, models.ChangeCreated))
		case string(prev.Payload) != string(row.Payload) || prev.Scope != row.Scope:
			changes = append(changes, change(row, models.ChangeUpdated))
		}
	}
	for key, row := range old {
		if _, ok := next[key]; !ok {
			changes = append(changes, change(row, models.ChangeDeleted))
		}
	}
	sortChanges(changes)
	return changes
}

func change(row models.ConfigItemRow, action string) models.ItemChange {
	return models.ItemChange{ID: itemKey(row.Kind, row.Name), Kind: row.Kind, Name: row.Name, Scope: row.Scope, Action: action}
}

func sortChanges(changes []models.ItemChange) {
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Scope != changes[j].Scope {
			return changes[i].Scope > changes[j].Scope // static first, as the review always listed them
		}
		if changes[i].Kind != changes[j].Kind {
			return changes[i].Kind < changes[j].Kind
		}
		return changes[i].Name < changes[j].Name
	})
}

func errNotFound() error { return fmt.Errorf("%w", files.ErrSourceFileNotFound) }
