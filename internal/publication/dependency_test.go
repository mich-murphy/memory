package publication

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

type failedDependencyBackend struct {
	Backend
	key    string
	cause  error
	onSync bool
}

func (b failedDependencyBackend) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if !b.onSync && key == b.key {
		return nil, b.cause
	}
	return b.Backend.Open(ctx, key)
}

func (b failedDependencyBackend) Sync(ctx context.Context, key string) error {
	if b.onSync && key == b.key {
		return b.cause
	}
	return fence(ctx, b.Backend, key)
}

func TestDependencyFailureCoordinatesAndClassification(t *testing.T) {
	b, m, _ := fixture(t)
	injected := errors.New("private/path?credential=value")
	for _, optional := range []bool{false, true} {
		m.Sources[0].Optional = optional
		if optional {
			m.Sources[0].Role = "artifact"
		}
		err := verify(t.Context(), failedDependencyBackend{Backend: b, key: m.Sources[0].Object.Key, cause: injected}, m)
		var dep *SourceDependencyError
		if !errors.As(err, &dep) || !errors.Is(err, injected) || dep.Optional != optional || dep.SourceKeyHash != SourceKeyHash(m.Sources[0]) || dep.EpochID != m.Sources[0].EpochID || dep.Stage != "verify" {
			t.Fatalf("dependency classification: %v", err)
		}
		if strings.Contains(err.Error(), "private") {
			t.Fatal("private error printed")
		}
	}
	c := publish(t, b, m, "")
	durability := errors.Join(ErrDurability, injected)
	for _, key := range []string{ArchiveKey, c.ManifestKey, m.Identity.CurrentKey(), m.Sources[0].Object.Key} {
		err := fencePublication(t.Context(), failedDependencyBackend{Backend: b, key: key, cause: durability, onSync: true}, m, c)
		var dep *SourceDependencyError
		typed := errors.As(err, &dep)
		if !errors.Is(err, ErrDurability) || typed != (key == m.Sources[0].Object.Key) {
			t.Fatalf("non-object fence misclassified: %v", err)
		}
	}
	if err := VerifySourceDependency(t.Context(), b, m.Sources[0]); err != nil {
		t.Fatal(err)
	}
	withheld, err := WithholdSource(m.Sources[0], "unavailable")
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifySourceDependency(t.Context(), failedDependencyBackend{Backend: b, key: m.Sources[0].Object.Key, cause: injected}, withheld); err != nil {
		t.Fatal("withheld object fetched", err)
	}
	if strings.Contains((&SourceDependencyError{Stage: "private/path", Err: injected}).Error(), "private") {
		t.Fatal("untrusted stage leaked")
	}
}
