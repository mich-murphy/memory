package publication

import (
	"context"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type exportReadCounter struct {
	Backend
	currents, bodies int
}

func (b *exportReadCounter) Current(ctx context.Context, key string) ([]byte, string, error) {
	b.currents++
	return b.Backend.Current(ctx, key)
}

func (b *exportReadCounter) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if strings.Contains(key, "/objects/") {
		b.bodies++
	}
	return b.Backend.Open(ctx, key)
}

func TestExportBoundedAggregateAndWithheld(t *testing.T) {
	for _, hide := range []bool{false, true} {
		b, m, optional := optionalFixture(t)
		if hide {
			m.Sources[1] = withheld(t, optional)
			m.Coverage.Missing = append(m.Coverage.Missing, MissingSourceWithheld)
		}
		publish(t, b, m, "")
		total := m.Sources[0].Object.Size
		if !hide {
			total += optional.Object.Size
		}
		counter := &exportReadCounter{Backend: b}
		dest := filepath.Join(t.TempDir(), "export")
		if _, err := ExportSessionBounded(t.Context(), counter, m.Identity, dest, total-1); !errors.Is(err, ErrExportBudget) {
			t.Fatal("under-budget export accepted", err)
		}
		if counter.bodies != 0 || counter.currents != 1 {
			t.Fatal("budget failure opened body or resolved twice")
		}
		if _, err := os.Stat(dest); !os.IsNotExist(err) {
			t.Fatal("budget failure created output")
		}
		counter.currents = 0
		out, err := ExportSessionBounded(t.Context(), counter, m.Identity, dest, total)
		if err != nil {
			t.Fatal(err)
		}
		expected := 2
		if hide {
			expected = 1
		}
		if len(out.Bodies) != expected || counter.bodies != expected || counter.currents != 1 {
			t.Fatal("export not pinned or selected incorrectly")
		}
		if _, err := os.Stat(filepath.Join(dest, "metadata.json")); err != nil {
			t.Fatal(err)
		}
	}
}

func TestExportBoundedInvalidAndExplicitLargeBudget(t *testing.T) {
	b, m, _ := fixture(t)
	publish(t, b, m, "")
	for _, limit := range []int64{-1, 0, MaxExportBytes + 1, math.MaxInt64} {
		counter := &exportReadCounter{Backend: b}
		dest := filepath.Join(t.TempDir(), "export")
		if _, err := ExportSessionBounded(t.Context(), counter, m.Identity, dest, limit); !errors.Is(err, ErrExportBudget) {
			t.Fatal("invalid budget accepted", err)
		}
		if counter.currents != 0 || counter.bodies != 0 {
			t.Fatal("invalid budget read backend")
		}
		if _, err := os.Stat(dest); !os.IsNotExist(err) {
			t.Fatal("invalid budget created output")
		}
	}
	if _, err := ExportSessionBounded(t.Context(), b, m.Identity, filepath.Join(t.TempDir(), "large"), MaxExportBytes); err != nil {
		t.Fatal(err)
	}
}

func TestExportBoundedPinsManifestAcrossBodyOpen(t *testing.T) {
	b, m, _ := fixture(t)
	publish(t, b, m, "")
	wrapper := &advanceOnOpen{Backend: b, t: t, next: m}
	wrapper.next.Sequence++
	wrapper.next.PublicationID = "next"
	counter := &exportReadCounter{Backend: wrapper}
	out, err := ExportSessionBounded(t.Context(), counter, m.Identity, filepath.Join(t.TempDir(), "export"), m.Sources[0].Object.Size)
	if err != nil || out.Manifest.PublicationID != m.PublicationID || counter.currents != 1 {
		t.Fatal("export re-resolved current", err)
	}
}
