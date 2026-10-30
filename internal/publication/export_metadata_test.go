package publication

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// envelopeFixture uses an independent wire oracle, with interleaved selected and
// withheld coordinates. Padding spans identities/epochs, not one oversized field.
func envelopeFixture(t *testing.T, size int) (*Local, Manifest, Export, []byte) {
	t.Helper()
	b, m, raw := fixture(t)
	s := m.Sources[0]
	m.Sources = nil
	m.Coverage.Missing = append(m.Coverage.Missing, MissingSourceWithheld)
	for n := range 4 {
		next := s
		next.SourceID = fmt.Sprintf("source-%d-雪<&\"-", n)
		next.EpochID = fmt.Sprintf("epoch-%d-雪<&\"-", n)
		if n%2 == 1 {
			next.Role, next.Optional = "artifact", true
			next = withheld(t, next)
		}
		m.Sources = append(m.Sources, next)
	}
	oracle := func() Export {
		return Export{Contract: ReaderContract, Manifest: m, Bodies: []Body{
			{SourceID: m.Sources[0].SourceID, EpochID: m.Sources[0].EpochID, Path: "bodies/0000.bin", SHA256: s.Object.SHA256, Size: s.Object.Size},
			{SourceID: m.Sources[2].SourceID, EpochID: m.Sources[2].EpochID, Path: "bodies/0002.bin", SHA256: s.Object.SHA256, Size: s.Object.Size},
		}}
	}
	// Selected IDs and epochs each appear twice; withheld epochs appear once.
	padding := strings.Repeat("x", (size-len(mustJSON(t, oracle())))/10)
	for n := range m.Sources {
		m.Sources[n].EpochID += padding
		if n%2 == 0 {
			m.Sources[n].SourceID += padding
		}
	}
	// Provenance appears only once, permitting exact odd/even byte boundaries.
	m.Sources[0].Path += strings.Repeat("p", size-len(mustJSON(t, oracle())))
	want := oracle()
	if got := len(mustJSON(t, want)); got != size {
		t.Fatalf("oracle size %d, want %d", got, size)
	}
	if len(mustJSON(t, m)) >= MaxMetadata {
		t.Fatal("fixture must isolate export aggregate, not manifest bound")
	}
	return b, m, want, raw
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestExportMetadataEnvelopeBoundaryJourney(t *testing.T) {
	for _, delta := range []int{-1, 0, 1} {
		t.Run(fmt.Sprintf("limit%+d", delta), func(t *testing.T) {
			b, m, want, raw := envelopeFixture(t, MaxMetadata+delta)
			if delta > 0 {
				if err := ValidateManifest(m); err == nil {
					t.Error("writer accepted an unexportable aggregate envelope")
				}
				counter := &metadataIOCounter{Backend: b}
				if _, err := Publish(t.Context(), counter, m, ""); err == nil {
					t.Error("Publish durably accepted an unexportable envelope")
				}
				if counter.calls != 0 {
					t.Errorf("inevitable writer rejection accessed backend %d times", counter.calls)
				}
				return
			}
			if err := ValidateManifest(m); err != nil {
				t.Fatal(err)
			}
			publish(t, b, m, "")
			got, err := Read(t.Context(), b, m.Identity)
			if err != nil || !reflect.DeepEqual(got, m) {
				t.Fatal("reader lost full manifest identity/closure", err)
			}
			counter := &exportReadCounter{Backend: b}
			dest := filepath.Join(t.TempDir(), "export")
			out, err := ExportSession(t.Context(), counter, m.Identity, dest)
			if err != nil {
				t.Fatal(err)
			}
			if counter.currents != 1 || counter.bodies != 2 || !reflect.DeepEqual(out, want) {
				t.Fatal("export changed identity, selected/withheld closure or pin")
			}
			metadata, err := os.ReadFile(filepath.Join(dest, "metadata.json"))
			if err != nil || !bytes.Equal(metadata, mustJSON(t, want)) {
				t.Fatal("metadata differs from exact boundary oracle", err)
			}
			for _, body := range out.Bodies {
				data, err := os.ReadFile(filepath.Join(dest, body.Path))
				if err != nil || !bytes.Equal(data, raw) {
					t.Fatal("selected body changed", err)
				}
			}
			entries, err := os.ReadDir(filepath.Join(dest, "bodies"))
			if err != nil || len(entries) != 2 {
				t.Fatal("withheld body invented", err)
			}
		})
	}
}

type metadataIOCounter struct {
	Backend
	calls int
}

func (b *metadataIOCounter) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	b.calls++
	return b.Backend.Open(ctx, key)
}

func (b *metadataIOCounter) Current(ctx context.Context, key string) ([]byte, string, error) {
	b.calls++
	return b.Backend.Current(ctx, key)
}

func (b *metadataIOCounter) PutFile(ctx context.Context, key, name string) error {
	b.calls++
	return b.Backend.PutFile(ctx, key, name)
}

func (b *metadataIOCounter) CompareAndSwap(ctx context.Context, key, token string, data []byte) error {
	b.calls++
	return b.Backend.CompareAndSwap(ctx, key, token, data)
}

func TestExportMetadataPreflightNoIO(t *testing.T) {
	b, m, _, _ := envelopeFixture(t, MaxMetadata+1)
	// Bypass writer acceptance to exercise export's own defensive preflight.
	counter := &exportReadCounter{Backend: b}
	dest := filepath.Join(t.TempDir(), "export")
	if _, err := exportPinned(t.Context(), counter, m, dest); err == nil {
		t.Error("oversized export accepted")
	}
	if counter.bodies != 0 {
		t.Errorf("inevitable metadata rejection fetched %d bodies", counter.bodies)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Error("inevitable metadata rejection created destination")
	}
}

func TestExportMetadataStoredOversizeNoIO(t *testing.T) {
	b, m, _, _ := envelopeFixture(t, MaxMetadata+1)
	// Model a manifest accepted by the old writer, without accepting it through
	// today's writer. The public reader/exporter must reject before body access.
	data := mustJSON(t, m)
	digest := token(data)
	key := m.Identity.BaseKey() + "/manifests/" + digest + ".json"
	if err := writeMetadata(t.Context(), b, key, data); err != nil {
		t.Fatal(err)
	}
	current := Current{
		Contract: Contract, Identity: m.Identity, PublicationID: m.PublicationID,
		Sequence: m.Sequence, ManifestKey: key, ManifestSHA256: digest,
	}
	if err := b.CompareAndSwap(t.Context(), m.Identity.CurrentKey(), "", mustJSON(t, current)); err != nil {
		t.Fatal(err)
	}
	counter := &exportReadCounter{Backend: b}
	dest := filepath.Join(t.TempDir(), "export")
	if _, err := ExportSession(t.Context(), counter, m.Identity, dest); err == nil {
		t.Error("public export accepted oversized metadata")
	}
	if counter.bodies != 0 || counter.currents != 1 {
		t.Error("inevitable metadata rejection read bodies or selected twice")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Error("inevitable metadata rejection created destination")
	}
}
