package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/mich-murphy/memory/internal/raw"
	"github.com/mich-murphy/memory/internal/testenv"
)

// These tests drive only the current v3 CLI and its private Local destination. The
// oracle checks physical immutable records and bytes, not the SQLite projection or
// Syncer.Restore. No production config, credentials, agent or hook is used.
// This remains legacy collector acceptance, not the replacement writer-reader-importer
// gate in docs/session-capture-contract.md; no shared production reader/importer is exercised.
type acceptanceRun struct {
	t      *testing.T
	root   string
	home   string
	work   string
	bucket string
	state  string
	env    []string
}

type acceptanceRecord struct {
	Schema    int             `json:"schema"`
	MachineID string          `json:"machine_id"`
	Kind      string          `json:"kind"`
	Agent     string          `json:"agent"`
	Key       string          `json:"key"`
	Sequence  uint64          `json:"sequence"`
	Parent    string          `json:"parent"`
	File      *raw.Generation `json:"file"`
	Manifest  struct {
		Info        map[string]any `json:"info"`
		Transcripts []struct {
			Key  string `json:"key"`
			Role string `json:"role"`
		} `json:"transcripts"`
	} `json:"manifest"`
	id string
}

func newAcceptanceRun(t *testing.T) *acceptanceRun {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Snapshots can consult host managed/ancestor instructions despite a fake
	// HOME. Check existence only and fail closed, never read those files.
	for _, path := range []string{"/etc/claude-code", "/etc/codex", "/Library/Application Support/ClaudeCode"} {
		acceptanceRequireAbsent(t, path)
	}
	for dir := filepath.Dir(root); ; dir = filepath.Dir(dir) {
		for _, name := range []string{"CLAUDE.md", "CLAUDE.local.md", "AGENTS.md", "AGENTS.override.md"} {
			acceptanceRequireAbsent(t, filepath.Join(dir, name))
		}
		if dir == filepath.Dir(dir) {
			break
		}
	}
	r := &acceptanceRun{t: t, root: root, home: filepath.Join(root, "home"), work: filepath.Join(root, "work"), bucket: filepath.Join(root, "bucket"), state: filepath.Join(root, "state")}
	for _, dir := range []string{r.home, r.work, r.bucket, r.state, filepath.Join(root, "tmp"), filepath.Join(root, "cover")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	cover := os.Getenv("E2E_GOCOVERDIR")
	if cover == "" {
		cover = filepath.Join(root, "cover")
	}
	// Deliberately not os.Environ(): inherited live/R2/provider/disable flags
	// cannot select another backend or silently bypass capture.
	r.env = []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + r.home, "TMPDIR=" + filepath.Join(root, "tmp"), "LANG=C", "SHELL=/bin/sh",
		"XDG_CONFIG_HOME=" + filepath.Join(r.home, ".config"), "XDG_CACHE_HOME=" + filepath.Join(r.home, ".cache"),
		"XDG_STATE_HOME=" + filepath.Join(r.home, ".local", "state"), "GOCOVERDIR=" + cover,
		"AGENT_SESSIONS_CONFIG=" + filepath.Join(root, "synthetic-config"), "AGENT_SESSIONS_STATE_DIR=" + r.state,
		"AGENT_SESSIONS_LOCAL_STORE=" + r.bucket, "AGENT_SESSIONS_MACHINE_ID=acceptance",
		"CLAUDE_CONFIG_DIR=" + filepath.Join(r.home, ".claude"), "CODEX_HOME=" + filepath.Join(r.home, ".codex"),
		"PI_CODING_AGENT_DIR=" + filepath.Join(r.home, ".pi", "agent"), "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
	}
	r.write(filepath.Join(root, "synthetic-config"), []byte("# Synthetic local acceptance only.\n"))
	return r
}

func acceptanceRequireAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("acceptance blocked: host managed/ancestor path exists or cannot be checked: %s", path)
	}
}

func (r *acceptanceRun) write(path string, body []byte) {
	r.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		r.t.Fatal(err)
	}
}

func (r *acceptanceRun) cli(args ...string) {
	r.t.Helper()
	cmd := exec.CommandContext(r.t.Context(), filepath.Join(binDir, "agent-sessions"), args...)
	cmd.Env, cmd.Dir = r.env, r.work
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("isolated agent-sessions %v: %v\n%s", args, err, out)
	}
}

func (r *acceptanceRun) object(key string) ([]byte, error) {
	if !raw.ValidKey(key) {
		return nil, errors.New("acceptance: unsafe object key")
	}
	return os.ReadFile(filepath.Join(r.bucket, filepath.FromSlash(key)))
}

func (r *acceptanceRun) inventory() map[string]string {
	r.t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(r.bucket, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !d.Type().IsRegular() {
			return errors.New("acceptance: nonregular bucket object")
		}
		key, err := filepath.Rel(r.bucket, path)
		if err != nil {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(key)] = raw.SHA256(body)
		return nil
	})
	if err != nil {
		r.t.Fatal(err)
	}
	return out
}

func (r *acceptanceRun) records(kind, agent, key string) []acceptanceRecord {
	r.t.Helper()
	prefix := "v3/machines/acceptance/records/" + kind + "/" + raw.SHA256([]byte(agent+"\x00"+key)) + "/"
	var keys []string
	for object := range r.inventory() {
		if strings.HasPrefix(object, prefix+"commits/") {
			keys = append(keys, object)
		}
	}
	slices.Sort(keys)
	var records []acceptanceRecord
	parent := ""
	for i, object := range keys {
		body, err := r.object(object)
		if err != nil {
			r.t.Fatal(err)
		}
		var record acceptanceRecord
		if err := json.Unmarshal(body, &record); err != nil {
			r.t.Fatal(err)
		}
		var wire livePublication
		if err := json.Unmarshal(body, &wire); err != nil {
			r.t.Fatal(err)
		}
		if err := liveValidateFileDependencies(wire); err != nil {
			r.t.Fatal(err)
		}
		record.id = raw.SHA256(body)
		name := fmt.Sprintf("%020d-%s.json", i+1, record.id)
		if record.Schema != 3 || record.MachineID != "acceptance" || record.Kind != kind || record.Agent != agent || record.Key != key || record.Sequence != uint64(i+1) || record.Parent != parent || object != prefix+"commits/"+name {
			r.t.Fatalf("invalid physical %s record chain at sequence %d", kind, i+1)
		}
		plan, err := r.object(prefix + "plans/" + name)
		if err != nil || !bytes.Equal(plan, body) {
			r.t.Fatalf("plan/commit differ at sequence %d: %v", i+1, err)
		}
		if kind == "file" && (record.File == nil || record.File.Sequence != record.Sequence || record.File.Parent != record.Parent || record.File.Identity.Key != key || record.File.Identity.Agent != agent || record.File.Identity.MachineID != "acceptance") {
			r.t.Fatalf("file envelope differs at sequence %d", i+1)
		}
		records = append(records, record)
		parent = record.id
	}
	return records
}

func (r *acceptanceRun) latest(kind, agent, key string) acceptanceRecord {
	r.t.Helper()
	records := r.records(kind, agent, key)
	if len(records) == 0 {
		r.t.Fatalf("missing committed %s %s:%s", kind, agent, key)
	}
	return records[len(records)-1]
}

func (r *acceptanceRun) content(record acceptanceRecord) []byte {
	r.t.Helper()
	// Content verifies encoded hashes/sizes, decoded ranges/hashes and whole
	// content identity. It does not assume redundant dependency arrays exist.
	body, err := testenv.Content(record.File, r.object)
	if err != nil {
		r.t.Fatal(err)
	}
	return body
}

func (r *acceptanceRun) history(agent, key string, want [][]byte) {
	r.t.Helper()
	records := r.records("file", agent, key)
	if len(records) != len(want) {
		r.t.Fatalf("history length = %d, want %d", len(records), len(want))
	}
	for i := range records {
		if !bytes.Equal(r.content(records[i]), want[i]) {
			r.t.Errorf("historical bytes differ at sequence %d", i+1)
		}
	}
}

func (r *acceptanceRun) retain(before map[string]string) {
	r.t.Helper()
	after := r.inventory()
	for key, sha := range before {
		if strings.HasSuffix(key, "/checkpoint.json") {
			continue // mutable recovery acceleration, not acknowledged history
		}
		if after[key] != sha {
			r.t.Errorf("acknowledged immutable object changed or disappeared: %s", key)
		}
	}
}

func (r *acceptanceRun) cold() {
	r.t.Helper()
	if err := os.RemoveAll(r.state); err != nil {
		r.t.Fatal(err)
	}
}

func (r *acceptanceRun) age(path string) {
	r.t.Helper()
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		r.t.Fatal(err)
	}
}

func (r *acceptanceRun) absentLiterals(literals ...string) {
	r.t.Helper()
	for key := range r.inventory() {
		body, err := r.object(key)
		if err != nil {
			r.t.Fatal(err)
		}
		if strings.HasSuffix(key, ".zst") {
			body, err = raw.Decompress(body)
			if err != nil {
				r.t.Fatal(err)
			}
		}
		for _, literal := range literals {
			if bytes.Contains(body, []byte(literal)) {
				r.t.Errorf("withheld synthetic literal reached archive object %s", key)
			}
		}
	}
}

func (r *acceptanceRun) unchanged(before map[string]string) {
	r.t.Helper()
	after := r.inventory()
	// Each successful lifecycle rotates this disposable hint, even without new data.
	// Everything else, including store identity, descriptors and blobs, must be identical.
	before = maps.Clone(before)
	delete(before, "v3/machines/acceptance/checkpoint.json")
	delete(after, "v3/machines/acceptance/checkpoint.json")
	if diff := cmp.Diff(before, after); diff != "" {
		r.t.Errorf("idle sync mutated archive (-want +got):\n%s", diff)
	}
}
