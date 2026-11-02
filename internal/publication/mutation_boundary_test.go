package publication

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMetadataLimitBoundaries(t *testing.T) {
	if !validStrings(strings.Repeat("x", MaxMetadata), "ok") {
		t.Fatal("string of exactly MaxMetadata bytes rejected")
	}
	if validStrings("ok", strings.Repeat("x", MaxMetadata+1)) {
		t.Fatal("string of MaxMetadata+1 bytes accepted")
	}
	payload := func(size int) []byte {
		const wrap = `{"k":""}`
		return []byte(`{"k":"` + strings.Repeat("x", size-len(wrap)) + `"}`)
	}
	var got map[string]string
	if err := decode(payload(MaxMetadata), &got); err != nil || len(got["k"]) != MaxMetadata-len(`{"k":""}`) {
		t.Fatalf("decode at limit: %v", err)
	}
	if err := decode(payload(MaxMetadata+1), &got); err == nil || err.Error() != "metadata exceeds limit" {
		t.Fatalf("decode over limit: %v", err)
	}
}

func TestReadMetadataLimitBoundaries(t *testing.T) {
	b, m, _ := fixture(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		size int
		bad  bool
	}{{"at-limit", MaxMetadata, false}, {"over-limit", MaxMetadata + 1, true}} {
		t.Run(tc.name, func(t *testing.T) {
			data := []byte(strings.Repeat("m", tc.size))
			key := m.Identity.BaseKey() + "/manifests/" + token(data) + ".json"
			p := filepath.Join(t.TempDir(), "meta")
			if err := os.WriteFile(p, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := b.PutFile(ctx, key, p); err != nil {
				t.Fatal(err)
			}
			got, err := readMetadata(ctx, b, key)
			if tc.bad {
				if !errors.Is(err, ErrInvalidMetadata) {
					t.Fatalf("over-limit metadata: %v", err)
				}
				return
			}
			if err != nil || !bytes.Equal(got, data) {
				t.Fatalf("at-limit metadata: len %d err %v", len(got), err)
			}
		})
	}
}

// A manifest of exactly MaxMetadata bytes fits its own bound but not the larger
// export envelope; one byte more fails the manifest bound itself.
func TestManifestSizeBoundaryReportsWhichBoundFailed(t *testing.T) {
	_, m, _ := fixture(t)
	m.Sources[0].Path = ""
	base := len(mustJSON(t, m))
	for _, tc := range []struct {
		size int
		want string
	}{{MaxMetadata, "export metadata exceeds limit"}, {MaxMetadata + 1, "metadata exceeds limit"}} {
		m.Sources[0].Path = strings.Repeat("p", tc.size-base)
		if got := len(mustJSON(t, m)); got != tc.size {
			t.Fatalf("fixture size %d, want %d", got, tc.size)
		}
		if err := ValidateManifest(m); err == nil || err.Error() != tc.want {
			t.Fatalf("size %d: %v, want %q", tc.size, err, tc.want)
		}
	}
}

func TestObjectDescriptorSizeBoundaries(t *testing.T) {
	_, m, _ := fixture(t)
	base := m.Sources[0].Object
	for _, tc := range []struct {
		name          string
		size, encoded int64
		ok            bool
	}{
		{"empty-body", 0, base.EncodedSize, true},
		{"zero-encoded", base.Size, 0, true},
		{"max-minus-one", math.MaxInt64 - 1, math.MaxInt64 - 1, true},
		{"negative-size", -1, base.EncodedSize, false},
		{"negative-encoded", base.Size, -1, false},
		{"max-size", math.MaxInt64, base.EncodedSize, false},
		{"max-encoded", base.Size, math.MaxInt64, false},
	} {
		o := base
		o.Size, o.EncodedSize = tc.size, tc.encoded
		if err := validateObjectDescriptor(o); (err == nil) != tc.ok {
			t.Fatalf("%s: %v", tc.name, err)
		}
	}
}

func TestAdvancesRequiresStrictlyHigherSequenceAndNewID(t *testing.T) {
	_, old, _ := fixture(t)
	old.Sequence = 7
	for _, tc := range []struct {
		name string
		seq  uint64
		id   string
		ok   bool
	}{
		{"same-sequence-new-id", 7, "pub-other", false},
		{"lower-sequence", 6, "pub-other", false},
		{"higher-sequence-same-id", 8, old.PublicationID, false},
		{"higher-sequence-new-id", 8, "pub-other", true},
	} {
		next := old
		next.Sequence, next.PublicationID = tc.seq, tc.id
		err := advances(old, next)
		if tc.ok != (err == nil) || (!tc.ok && !errors.Is(err, ErrConflict)) {
			t.Fatalf("%s: %v", tc.name, err)
		}
	}
}

// installRaw stores arbitrary manifest bytes under their content address and
// points current at them, so Read exercises the resolve checks in isolation.
func installRaw(t *testing.T, b *Local, i Identity, data []byte, id string, seq uint64) {
	t.Helper()
	ctx := context.Background()
	digest := token(data)
	c := Current{Contract: Contract, Identity: i, PublicationID: id, Sequence: seq, ManifestSHA256: digest, ManifestKey: i.BaseKey() + "/manifests/" + digest + ".json"}
	p := filepath.Join(t.TempDir(), "manifest")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := b.PutFile(ctx, c.ManifestKey, p); err != nil {
		t.Fatal(err)
	}
	cur, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err = b.CompareAndSwap(ctx, i.CurrentKey(), "", cur); err != nil {
		t.Fatal(err)
	}
}

func TestReadRejectsCurrentManifestsFailingResolveChecks(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Manifest)
		raw    func([]byte) []byte
		id     string
		seq    uint64
		want   string
	}{
		{name: "trailing-bytes", raw: func(d []byte) []byte { return append(d, " {}"...) }, want: "trailing metadata"},
		{name: "invalid-manifest", mutate: func(m *Manifest) { m.ObservedAtNS = -1 }, want: "negative manifest timestamp"},
		{name: "publication-mismatch", id: "pub-other", want: "manifest/current mismatch"},
		{name: "sequence-mismatch", seq: 2, want: "manifest/current mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, m, _ := fixture(t)
			if tc.mutate != nil {
				tc.mutate(&m)
			}
			data := mustJSON(t, m)
			if tc.raw != nil {
				data = tc.raw(data)
			}
			id, seq := m.PublicationID, m.Sequence
			if tc.id != "" {
				id = tc.id
			}
			if tc.seq != 0 {
				seq = tc.seq
			}
			installRaw(t, b, m.Identity, data, id, seq)
			if _, err := Read(context.Background(), b, m.Identity); err == nil || err.Error() != tc.want {
				t.Fatalf("Read: %v, want %q", err, tc.want)
			}
		})
	}
}
