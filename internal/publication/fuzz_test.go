package publication

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

func FuzzParseCurrent(f *testing.F) {
	h := strings.Repeat("ab", 32)
	id := Identity{ArchiveID: "a", MachineID: "m", Agent: "pi", SessionID: "s"}
	mk := func(i Identity, seq uint64, sha string) []byte {
		c := Current{Contract: Contract, Identity: i, PublicationID: "p", ManifestKey: id.BaseKey() + "/manifests/" + sha + ".json", ManifestSHA256: sha, Sequence: seq}
		b, _ := json.Marshal(c)
		return b
	}
	f.Add(mk(id, 1, h), "a", "m", "pi", "s")
	f.Add(mk(id, 1, h), "a", "m", "pi", "other")
	f.Add(mk(id, 0, h), "a", "m", "pi", "s")
	f.Add(mk(id, 1<<63, h), "a", "m", "pi", "s")
	f.Add(mk(id, 1, strings.ToUpper(h)), "a", "m", "pi", "s")
	f.Add(append(mk(id, 1, h), []byte(" {}")...), "a", "m", "pi", "s")
	f.Add(append([]byte("\xef\xbb\xbf"), mk(id, 1, h)...), "a", "m", "pi", "s")
	f.Add([]byte(`{"sequence":-1}`), "a", "m", "pi", "s")
	f.Add([]byte(strings.Repeat("[", 5000)), "a", "m", "pi", "s")
	f.Fuzz(func(t *testing.T, data []byte, a, m, ag, s string) {
		i := Identity{ArchiveID: a, MachineID: m, Agent: ag, SessionID: s}
		c, err := parseCurrent(data, i)
		c2, err2 := parseCurrent(data, i)
		if (err == nil) != (err2 == nil) || (err == nil && c != c2) {
			t.Fatal("nondeterministic decision")
		}
		if err != nil {
			return
		}
		if !validOrder(c.Sequence) || c.Sequence == 0 || c.Sequence > 1<<63-1 {
			t.Fatalf("sequence %d", c.Sequence)
		}
		if len(c.ManifestSHA256) != 64 || strings.ToLower(c.ManifestSHA256) != c.ManifestSHA256 || strings.Trim(c.ManifestSHA256, "0123456789abcdef") != "" {
			t.Fatalf("digest %q", c.ManifestSHA256)
		}
		if c.Identity != i || c.ManifestKey != i.BaseKey()+"/manifests/"+c.ManifestSHA256+".json" {
			t.Fatal("not bound to identity")
		}
	})
}

func FuzzDecodeValidateManifest(f *testing.F) {
	h := strings.Repeat("cd", 32)
	id := Identity{ArchiveID: "a", MachineID: "m", Agent: "pi", SessionID: "s"}
	src := Source{
		SourceID: "src", EpochID: "e", Role: "transcript", Path: "p", State: "captured", Revision: 1, ObservedAtNS: 1, CapturedAtNS: 1,
		Object:       Object{Key: id.BaseKey() + "/objects/" + h + ".zst", Encoding: "zstd", SHA256: h, EncodedSHA256: h, Size: 3, EncodedSize: 9},
		Associations: []string{"x"},
	}
	base := Manifest{Contract: Contract, Identity: id, PublicationID: "p", Sequence: 1, Coverage: Coverage{Discovery: "complete", Enrichment: "complete", Missing: []string{}}, Sources: []Source{src}}
	add := func(m Manifest) { b, _ := json.Marshal(m); f.Add(b) }
	add(base)
	e := base
	e.Sources = nil
	add(e)
	w := base
	w.Coverage = Coverage{Discovery: "complete", Enrichment: "partial", Missing: []string{MissingSourceWithheld}}
	w.Sources = []Source{{SourceID: "", EpochID: "e", Role: "transcript", Path: "p", State: "withheld", Revision: 1, Withheld: &WithheldSource{SourceIDHash: h, SnapshotCommitment: h, Reason: "r"}, Associations: []string{}}}
	add(w)
	o := base
	o.Sequence = 1 << 63
	add(o)
	f.Add([]byte(`{"contract":"session-publication/v2","sequence":9223372036854775808}`))
	f.Add([]byte("{\"publication_id\":\"\\ud800\"}"))
	f.Add([]byte(`{"sources":[` + strings.Repeat(`{},`, 2000) + `{}]}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		var m Manifest
		if decode(data, &m) != nil || ValidateManifest(m) != nil {
			return
		}
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		var m2 Manifest
		if err := decode(b, &m2); err != nil {
			t.Fatalf("re-decode: %v", err)
		}
		if err := ValidateManifest(m2); err != nil {
			t.Fatalf("re-validate: %v", err)
		}
		if d := cmp.Diff(m, m2, cmpopts.EquateEmpty()); d != "" {
			t.Fatal(d)
		}
		b2, _ := json.Marshal(m2)
		if !bytes.Equal(b, b2) {
			t.Fatal("marshal not stable")
		}
	})
}
