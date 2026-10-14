// Package launch defines the durable handoff from a fast hook to archive publication.
package launch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
	"math"
	"unicode/utf8"

	"github.com/mich-murphy/memory/internal/jsonx"
)

const Schema = 1

type Envelope struct {
	Schema     int            `json:"schema"`
	ID         string         `json:"id"`
	Agent      string         `json:"agent"`
	Path       string         `json:"path"`
	RecordedAt float64        `json:"recorded_at"`
	Payload    map[string]any `json:"payload"`
}

func (e Envelope) Validate() error {
	if !utf8.ValidString(e.ID) || !utf8.ValidString(e.Path) {
		return errors.New("launch: unsupported non-UTF-8 identity or path")
	}
	if e.Schema != Schema || e.ID == "" || e.Path == "" || e.Payload == nil || math.IsNaN(e.RecordedAt) || math.IsInf(e.RecordedAt, 0) || e.RecordedAt < 0 {
		return errors.New("launch: invalid envelope")
	}
	switch e.Agent {
	case "claude", "codex", "pi":
	default:
		return errors.New("launch: unknown agent")
	}
	return nil
}

// Map is the temporary bridge to the legacy manifest boundary. Routing stays outside payload.
func (e Envelope) Map() map[string]any {
	m := maps.Clone(e.Payload)
	m["recorded_at"] = e.RecordedAt
	return m
}

// Decode accepts the earlier flat queue format as well as typed envelopes. Legacy queue
// identity hashes its exact durable bytes, so publication retries keep the same identity.
func Decode(body []byte) (Envelope, error) {
	var e Envelope
	if err := jsonx.Decode(body, &e); err != nil {
		return e, err
	}
	if e.Schema == 0 {
		var legacy struct {
			Agent      string  `json:"agent"`
			Path       string  `json:"path"`
			RecordedAt float64 `json:"recorded_at"`
		}
		if err := json.Unmarshal(body, &legacy); err != nil {
			return e, err
		}
		payload, ok := jsonx.Map(body)
		if !ok {
			return e, errors.New("launch: expected object")
		}
		delete(payload, "agent")
		delete(payload, "path")
		delete(payload, "recorded_at")
		sum := sha256.Sum256(body)
		e = Envelope{Schema: Schema, ID: hex.EncodeToString(sum[:]), Agent: legacy.Agent, Path: legacy.Path, RecordedAt: legacy.RecordedAt, Payload: payload}
	}
	return e, e.Validate()
}
