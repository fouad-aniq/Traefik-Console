// Package enginetest holds test doubles for the operation engine. It is imported by tests only.
package enginetest

import (
	"context"

	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/models"
)

// DirectApplier commits a mutation straight to the store: no rendering, no files, no Traefik.
// Resource-manager tests use it to exercise manager logic without the publish pipeline.
type DirectApplier struct {
	Store db.Store
	// Applied records every mutation in order.
	Applied []models.Mutation
	// Err, when set, fails Apply.
	Err error
}

func (d *DirectApplier) Apply(ctx context.Context, m models.Mutation) (*models.OperationResult, error) {
	if d.Err != nil {
		return nil, d.Err
	}
	d.Applied = append(d.Applied, m)
	written, e := d.Store.CommitOperation(ctx, db.Commit{Mutation: m})
	if e != nil {
		return nil, e
	}
	return &models.OperationResult{Operation: &models.OperationRow{Status: models.OpStatusSucceeded}, Items: written}, nil
}

func (d *DirectApplier) Preview(context.Context, models.Mutation) (*models.OperationPreview, error) {
	return &models.OperationPreview{}, nil
}
