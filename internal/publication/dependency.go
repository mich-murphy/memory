package publication

import (
	"context"
	"io"
)

// SourceDependencyError identifies a failure at one selected object's boundary,
// without including its path, key, descriptor, body or underlying private error in
// diagnostics. Unwrap preserves cancellation, durability and transport classifiers.
// It must never wrap archive, manifest, current-reference or source-order failures.
type SourceDependencyError struct {
	SourceKeyHash string
	EpochID       string
	Optional      bool
	Stage         string
	Err           error
}

func (e *SourceDependencyError) Error() string {
	switch e.Stage {
	case "upload", "verify", "object-fence":
		return "publication: source dependency failed (" + e.Stage + ")"
	default:
		return "publication: source dependency failed"
	}
}
func (e *SourceDependencyError) Unwrap() error { return e.Err }

func dependencyError(s Source, stage string, err error) error {
	if err == nil {
		return nil
	}
	return &SourceDependencyError{SourceKeyHash: SourceKeyHash(s), EpochID: s.EpochID, Optional: s.Optional, Stage: stage, Err: err}
}

// VerifySourceDependency checks the exact remote immutable object described by a
// source from a previously validated manifest; withheld coordinates have no body.
func VerifySourceDependency(ctx context.Context, b Backend, s Source) error {
	if !IsSelected(s) {
		return nil
	}
	return dependencyError(s, "verify", streamObject(ctx, b, s.Object, io.Discard))
}
