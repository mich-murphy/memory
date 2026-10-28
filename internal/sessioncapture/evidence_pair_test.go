package sessioncapture

import (
	"errors"
	"testing"

	"github.com/mich-murphy/memory/internal/publication"
)

func TestEvidencePairSwappedAndWithheldCommitment(t *testing.T) {
	dir, opts, req := setup(t)
	q := open(t, dir, opts)
	capture(t, q, req)
	second := req
	second.SourceID = "other"
	second.Role = "artifact"
	second.Optional = true
	m := capture(t, q, second)
	original, es, err := q.latest(t.Context(), req.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 2 {
		t.Fatalf("sources: %d", len(es))
	}
	es[0], es[1] = es[1], es[0]
	if _, _, _, err = encodeAcceptedView(original, es); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("swapped encode: %v", err)
	}
	if err = validateEvidencePair(m, es); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("swapped decode: %v", err)
	}
	es[0], es[1] = es[1], es[0]
	withheld, err := publication.WithholdSource(m.Sources[1], "policy")
	if err != nil {
		t.Fatal(err)
	}
	m.Sources[1] = withheld
	copySource := original.Sources[1]
	es[1].Original = &copySource
	if err = validateEvidencePair(m, es); err != nil {
		t.Fatalf("valid withheld: %v", err)
	}
	es[1].Original.Object.SHA256 = "wrong"
	if err = validateEvidencePair(m, es); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("withheld commitment: %v", err)
	}
}
