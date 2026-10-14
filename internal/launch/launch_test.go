package launch

import (
	"testing"

	"github.com/mich-murphy/memory/internal/jsonx"
)

func TestEnvelopeRoundTripAndLegacyStableIdentity(t *testing.T) {
	e := Envelope{Schema: Schema, ID: "id", Agent: "pi", Path: "/p", RecordedAt: 12.75, Payload: map[string]any{"event": "session_start"}}
	body, err := jsonx.Compact(e)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(body)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != e.ID || got.RecordedAt != e.RecordedAt || got.Map()["event"] != "session_start" || got.Map()["recorded_at"] != 12.75 {
		t.Fatalf("Decode = %+v", got)
	}
	legacy := []byte(`{"agent":"pi","path":"/p","recorded_at":10.5,"event":"session_start"}`)
	a, err := Decode(legacy)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Decode(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == "" || a.ID != b.ID || a.RecordedAt != 10.5 || a.Map()["agent"] != nil {
		t.Fatalf("legacy = %+v", a)
	}
	for _, bad := range []string{"not json", `[]`, `{"agent":"other","path":"/p"}`, `{"agent":"pi"}`, `{"agent":"pi","path":"/p","recorded_at":"bad"}`, `{"schema":2,"id":"id","agent":"pi","path":"/p","payload":{}}`} {
		if _, err := Decode([]byte(bad)); err == nil {
			t.Errorf("Decode(%s) accepted", bad)
		}
	}
}
