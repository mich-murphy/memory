package launch

import (
	"strings"
	"testing"
)

func TestEnvelopeRejectsNonUTF8IdentifiersAndPaths(t *testing.T) {
	baseline := Envelope{Schema: Schema, ID: "id", Agent: "pi", Path: "/supported/café", Payload: map[string]any{}}
	if err := baseline.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := string([]byte{0xff})
	for _, mutate := range []func(*Envelope){func(e *Envelope) { e.ID += bad }, func(e *Envelope) { e.Path += bad }} {
		e := baseline
		mutate(&e)
		if err := e.Validate(); err == nil || !strings.Contains(err.Error(), "non-UTF-8") {
			t.Fatalf("Envelope.Validate = %v, want unsupported identity error", err)
		}
	}
}
