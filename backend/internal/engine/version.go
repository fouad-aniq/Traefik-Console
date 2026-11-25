package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"regexp"

	"github.com/traefik-console/internal/models"
)

type versionContextKey struct{}
type requestKeyContextKey struct{}

// ResourceVersion is the optimistic-concurrency precondition of one write.
type ResourceVersion struct {
	Ref models.ItemRef
	// Version is the version the client read. 0 with Create set means "must not exist".
	Version int64
	Create  bool
}

// WithExpectedVersion carries the API precondition (If-Match "<version>" or
// If-None-Match: *) into the serialized operation boundary, so the check cannot
// race with the write it protects.
func WithExpectedVersion(ctx context.Context, ref models.ItemRef, version int64, create bool) context.Context {
	return context.WithValue(ctx, versionContextKey{}, ResourceVersion{Ref: ref, Version: version, Create: create})
}

// WithRequestKey carries the Idempotency-Key header. A replay with the same key
// returns the stored operation instead of applying the change twice.
func WithRequestKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, requestKeyContextKey{}, key)
}

var requestKeyPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{16,128}$`)

// requestKeyFrom returns the caller's key, or a fresh random one when none was sent.
func requestKeyFrom(ctx context.Context) (key string, supplied bool, err error) {
	if v, ok := ctx.Value(requestKeyContextKey{}).(string); ok && v != "" {
		if !requestKeyPattern.MatchString(v) {
			return "", true, ErrPreconditionRequired
		}
		return v, true, nil
	}
	var random [16]byte
	if _, e := rand.Read(random[:]); e != nil {
		return "", false, e
	}
	return "op-" + hex.EncodeToString(random[:]), false, nil
}

// applyContextVersion copies the context precondition into the mutation.
func applyContextVersion(ctx context.Context, m *models.Mutation) {
	v, ok := ctx.Value(versionContextKey{}).(ResourceVersion)
	if !ok {
		return
	}
	if m.ExpectedVersions == nil {
		m.ExpectedVersions = map[string]int64{}
	}
	expected := v.Version
	if v.Create {
		expected = 0
	}
	m.ExpectedVersions[v.Ref.Key()] = expected
}

// checkVersions enforces ExpectedVersions against the committed rows.
func checkVersions(m models.Mutation, before map[string]models.ConfigItemRow) error {
	for key, expected := range m.ExpectedVersions {
		row, exists := before[key]
		switch {
		case expected == 0 && exists:
			return ErrVersionConflict
		case expected > 0 && (!exists || row.Version != expected):
			return ErrVersionConflict
		}
	}
	return nil
}
