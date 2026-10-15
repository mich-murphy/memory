package syncer

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/raw"
)

func TestSessionMetadataUsesBoundedTailAndKeepsObservedModel(t *testing.T) {
	sy := &Syncer{adapters: adapters.All(t.TempDir())}
	sf := adapters.SessionFile{Agent: "pi", Role: adapters.RoleMain}
	header := `{"type":"session","id":"own","timestamp":"2026-10-11T12:00:00Z"}` + "\n" + `{"type":"model_change","modelId":"initial"}` + "\n"
	tail := `{"type":"message","timestamp":"2026-10-11T12:00:05Z","message":{"model":"latest"}}` + "\n"
	data := []byte(header + strings.Repeat("x", metadataWindow+5) + "\n" + tail)
	c := &content{first: []byte(header), last: metadataTail(data, 0)}
	got := sy.sessionHead(sf, c, nil)
	if got["model"] != "latest" || got["last_event_at"] != "2026-10-11T12:00:05Z" || got["started_at"] != "2026-10-11T12:00:00Z" {
		t.Fatalf("sessionHead = %v", got)
	}
	if got["metadata_version"] != json.Number("1") {
		t.Errorf("metadata_version = %v", got["metadata_version"])
	}
	prev := &raw.Generation{Head: got}
	c.appended = true
	c.last = []byte("\nmalformed\n{\"type\":\"message\",\"timestamp\":")
	got = sy.sessionHead(sf, c, prev)
	if got["model"] != "latest" || got["last_event_at"] != "2026-10-11T12:00:05Z" {
		t.Fatalf("append sessionHead = %v", got)
	}
	c.appended = false
	if got = sy.sessionHead(sf, c, prev); got["model"] != "initial" {
		t.Errorf("rewrite model = %v, want initial", got["model"])
	}
	// A later event without a model keeps the known model, but advances observation time.
	c.appended = true
	c.last = []byte(`{"type":"message","timestamp":"2026-10-11T12:00:06Z"}` + "\n")
	got = sy.sessionHead(sf, c, prev)
	if got["model"] != "latest" || got["last_event_at"] != "2026-10-11T12:00:06Z" {
		t.Fatalf("model-free append = %v", got)
	}
}

func TestMetadataTailBoundariesAndStreamedCaptureAgree(t *testing.T) {
	for _, tc := range []struct {
		name   string
		data   []byte
		offset int64
		want   string
	}{
		{"empty", nil, 0, ""},
		{"huge", bytes.Repeat([]byte("x"), metadataWindow+10), 0, ""},
		{"fragment", []byte("crossing\n{}\n"), 20, "{}\n"},
		{"boundary", []byte("\n{}\n"), 20, "{}\n"},
		{"small", []byte("{}\n"), 0, "{}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(metadataTail(tc.data, tc.offset)); got != tc.want {
				t.Errorf("metadataTail = %q, want %q", got, tc.want)
			}
		})
	}
	path := filepath.Join(t.TempDir(), "captured.jsonl")
	data := []byte(`{"type":"session","id":"own"}` + "\n" + strings.Repeat("x", metadataWindow+32) + "\n" + `{"type":"message","timestamp":9007199254740993,"message":{"model":"tail"}}` + "\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	sy := &Syncer{adapters: adapters.All(t.TempDir())}
	streamed, err := sy.streamedContent(path, st, nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	sf := adapters.SessionFile{Agent: "pi", Path: path, Role: adapters.RoleMain}
	memory, err := inMemoryContent(sf, st, nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(sy.sessionHead(sf, memory, nil), sy.sessionHead(sf, streamed, nil)); diff != "" {
		t.Errorf("streamed metadata (-memory +streamed):\n%s", diff)
	}
	if got := sy.sessionHead(sf, streamed, nil)["last_event_at"]; got != json.Number("9007199254740993") {
		t.Errorf("last_event_at = %v", got)
	}
}

func TestMetadataTailUsesLatestLines(t *testing.T) {
	sy := &Syncer{adapters: adapters.All(t.TempDir())}
	body := strings.Repeat("{}\n", 300) + `{"type":"model_change","modelId":"latest"}` + "\n"
	got := sy.sessionHead(adapters.SessionFile{Agent: "pi"}, &content{last: []byte(body)}, nil)
	if got["model"] != "latest" {
		t.Errorf("tail model = %v, want latest", got["model"])
	}
	if _, ok := got["started_at"]; ok {
		t.Errorf("started_at = %v, want absent", got["started_at"])
	}
}

func TestUnchangedSessionMetadataDoesNotPublishGenerations(t *testing.T) {
	sy, cfg, db, bucket := contractArchive(t)
	sy.adapters = adapters.All(cfg.Home)
	path := filepath.Join(cfg.Home, ".pi", "agent", "sessions", "--work--", "2026-10-11T12-00-00.000Z_11111111-2222-3333-4444-555555555555.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"type":"session","id":"own","timestamp":"2026-10-11T12:00:00Z"}` + "\n" + `{"type":"model_change","modelId":"model"}` + "\n")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	sf, ok := sy.adapters["pi"].Classify(path, sy.adapters["pi"].CanonicalRoot())
	if !ok {
		t.Fatal("Classify rejected main")
	}
	sum, err := sy.Run(t.Context(), []adapters.SessionFile{sf})
	if err != nil || sum.Errors != 0 {
		t.Fatalf("Run = %+v, %v", sum, err)
	}
	_, before, err := sy.current("file", "pi", sf.Key)
	if err != nil {
		t.Fatal(err)
	}
	// Force re-reading identical bytes after decoding persisted json.Number metadata.
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, st.ModTime().Add(time.Second), st.ModTime().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	sy = New(cfg, bucket, db, adapters.All(cfg.Home), Options{})
	sum, err = sy.Run(t.Context(), []adapters.SessionFile{sf})
	if err != nil || sum.Errors != 0 {
		t.Fatalf("second Run = %+v, %v", sum, err)
	}
	_, after, err := sy.current("file", "pi", sf.Key)
	if err != nil {
		t.Fatal(err)
	}
	if before.ID != after.ID || sum.Uploaded != 0 || sum.Manifests != 0 {
		t.Fatalf("unchanged metadata published: before %s after %s summary %+v", before.ID, after.ID, sum)
	}
}
