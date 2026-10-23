package capturehooks

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/mich-murphy/memory/internal/capturepolicy"
	"github.com/mich-murphy/memory/internal/capturesource"
	"github.com/mich-murphy/memory/internal/publication"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

// Exercise actual producer output, discovery, queue preparation/publication and
// shared-reader export. Never synthesize a transform to hide filename mistakes.
func TestNativeBackupNeverPublishesRawSettings(t *testing.T) {
	o := sandbox(t)
	root := filepath.Join(o.Home, ".claude")
	o.Roots = []Root{{Agent: "claude", Path: root}}
	settings := filepath.Join(root, "settings.json")
	const original = `{"env":{"TOKEN":"SYNTHETIC_PRIVATE_TOKEN"},"cleanupPeriodDays":7,"hooks":{"Stop":[{"hooks":[{"type":"command","command":"foreign"}]}]}}`
	put(t, settings, original, 0o644)
	apply(t, build(t, o))
	backups, err := filepath.Glob(settings + ".agent-sessions-backup-*")
	if err != nil || len(backups) != 1 {
		t.Fatalf("backup count=%d err=%v", len(backups), err)
	}
	path := backups[0]
	if !bytes.Equal(get(t, path), []byte(original)) {
		t.Fatal("backup did not preserve original")
	}
	if capturepolicy.BaseName(path) != "settings.json" {
		t.Fatal("backup is outside policy filename category")
	}
	// Actual producer backups for primary credentials must stay withheld, not
	// acquire the narrowly scoped primary-config export exception.
	for _, name := range []string{".claude.json", ".credentials.json", "auth.json"} {
		protected := filepath.Join(root, name)
		put(t, protected, `{"TOKEN":"SYNTHETIC_CREDENTIAL"}`, 0o600)
		if err := backup(entry{path: protected, before: get(t, protected)}); err != nil {
			t.Fatal(err)
		}
	}
	state := filepath.Join(o.Home, "spool")
	policy, err := capturepolicy.New(o.ConfigPath, state)
	if err != nil {
		t.Fatal(err)
	}
	scope := func(_ context.Context, s capturesource.Scope) error {
		if policy.Excludes(s.Identities) {
			return errors.New("excluded")
		}
		return nil
	}
	opts := capturesource.Options{Home: o.Home, ArchiveID: "archive", MachineID: "machine", OnlyRoots: true, Roots: []capturesource.Root{{Agent: "claude", Path: root}}, Derived: true, ConfigPath: o.ConfigPath, StateDir: state, Policy: scope}
	found, err := capturesource.Discover(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := found.Cursor.Close(); err != nil {
			t.Error(err)
		}
	}()
	var request sessioncapture.Request
	for _, r := range found.Requests {
		if capturepolicy.IsCredential(r.Path) {
			t.Fatal("credential or primary backup admitted")
		}
		if r.Path == path {
			request = r
		}
	}
	if request.Path == "" || request.Transform != "scrubbed-config" || request.Role != "artifact" {
		t.Fatal("actual backup not discovered as scrubbed config")
	}
	// Non-derived discovery may withhold this artifact, but never admit it raw.
	opts.Derived = false
	if r, err := capturesource.Classify(t.Context(), opts, "claude", path); err == nil && r.Transform == "" {
		t.Fatal("backup admitted raw without derived support")
	}
	q, err := sessioncapture.Open(t.Context(), state, sessioncapture.Options{ArchiveID: "archive", MaxSpoolBytes: 1 << 20, CheckPolicy: func(ctx context.Context, r sessioncapture.Request) error {
		resolution, err := capturepolicy.Resolve(r.Path)
		if err != nil {
			return err
		}
		return scope(ctx, capturesource.Scope{Path: r.Path, Identities: resolution.Identities})
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := q.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := q.Capture(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	backend := publication.NewLocal(filepath.Join(o.Home, "remote"))
	if err := publication.WriteArchive(t.Context(), backend, publication.Archive{Contract: publication.Contract, ArchiveID: "archive"}); err != nil {
		t.Fatal(err)
	}
	if err := q.Deliver(t.Context(), backend, request.Identity); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(o.Home, "restored")
	exported, err := publication.ExportSession(t.Context(), backend, request.Identity, dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(exported.Bodies) != 1 {
		t.Fatal("unexpected body count")
	}
	restored := get(t, filepath.Join(dest, exported.Bodies[0].Path))
	for _, forbidden := range []string{"SYNTHETIC_PRIVATE_TOKEN"} {
		if bytes.Contains(restored, []byte(forbidden)) {
			t.Fatal("private env survived reader restoration")
		}
	}
	for _, retained := range []string{`"TOKEN":"[redacted]"`, `"cleanupPeriodDays":7`, `"foreign"`} {
		if !bytes.Contains(restored, []byte(retained)) {
			t.Fatal("scrub lost unrelated settings or hooks")
		}
	}
	if !bytes.Equal(get(t, path), []byte(original)) {
		t.Fatal("capture changed original backup")
	}
}
