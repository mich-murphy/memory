package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/capturedaemon"
	"github.com/mich-murphy/memory/internal/capturesource"
	"github.com/mich-murphy/memory/internal/exclusion"
	"github.com/mich-murphy/memory/internal/publication"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

// Synthetic files only; no native agents, cloud or private sources.
func TestAdversarialPiIdentityMutation(t *testing.T) {
	const parent = "2026-10-29T00-00-00Z_11111111-1111-1111-1111-111111111111"
	for _, local := range []string{parent + ".jsonl", filepath.Join(parent, "tasks", "child.jsonl"), filepath.Join(parent, "forks", "child.jsonl"), filepath.Join(parent, "22222222-2222-2222-2222-222222222222", "run-1", "session.jsonl")} {
		t.Run(local, func(t *testing.T) {
			c, config := runtimeFixture(t)
			root := filepath.Join(c.Home, "flat-pi")
			path := filepath.Join(root, local)
			runtimeWrite(t, path)
			c.DefaultRoots = false
			c.Roots = []capturedaemon.Root{{Agent: "pi", Path: root}, {Agent: "claude", Path: filepath.Join(c.Home, ".claude")}}
			if err := capturedaemon.Save(config, c); err != nil {
				t.Fatal(err)
			}
			loaded, err := capturedaemon.Load(config)
			if err != nil {
				t.Fatal(err)
			}
			r, err := newRuntimeComposition(loaded)
			if err != nil {
				t.Fatal(err)
			}
			before, err := capturesource.Classify(t.Context(), r.sourceOptions(config), "pi", path)
			if err != nil {
				t.Fatal(err)
			}
			want := "subagent"
			if local == parent+".jsonl" {
				want = "main"
			}
			if before.Role != want {
				t.Fatalf("role=%s want %s", before.Role, want)
			}
			q, err := sessioncapture.Open(t.Context(), c.Spool, r.queueOptions())
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := q.Close(); err != nil {
					t.Error(err)
				}
			}()
			first, err := q.Capture(t.Context(), before)
			if err != nil {
				t.Fatal(err)
			}
			if first.Identity != before.Identity || len(first.Sources) != 1 || first.Sources[0].EpochID == "" {
				t.Fatalf("invalid initial capture: %#v", first)
			}
			previous := first.Sources[0]
			check := func(stage string) {
				t.Helper()
				current, err := newRuntimeComposition(loaded)
				if err != nil {
					t.Fatal(err)
				}
				after, err := capturesource.Classify(t.Context(), current.sourceOptions(config), "pi", path)
				if err != nil {
					t.Fatalf("%s: %v", stage, err)
				}
				if before.SourceID != after.SourceID || before.Identity != after.Identity || before.Role != after.Role {
					t.Fatalf("%s changed identity/role: %#v -> %#v", stage, before, after)
				}
				if err := current.policy(false)(t.Context(), before); err != nil {
					t.Fatalf("%s rejected previously queued identity: %v", stage, err)
				}

				// Reopen using the current composition's actual queue policy, append real
				// bytes, and prove the retained source epoch advances rather than splits.
				if err := q.Close(); err != nil {
					t.Fatal(err)
				}
				q, err = sessioncapture.Open(t.Context(), c.Spool, current.queueOptions())
				if err != nil {
					t.Fatal(err)
				}
				runtimeAppendSynthetic(t, path)
				after, err = capturesource.Classify(t.Context(), current.sourceOptions(config), "pi", path)
				if err != nil {
					t.Fatal(err)
				}
				manifest, err := q.Capture(t.Context(), after)
				if err != nil {
					t.Fatalf("%s append capture: %v", stage, err)
				}
				if manifest.Identity != first.Identity || len(manifest.Sources) != 1 {
					t.Fatalf("%s split session or source: %#v", stage, manifest)
				}
				source := manifest.Sources[0]
				if source.SourceID != previous.SourceID || source.SourceID != before.SourceID || source.EpochID != previous.EpochID || source.Role != previous.Role || source.Role != want || source.Revision != previous.Revision+1 || source.Object.Size != previous.Object.Size+int64(len("synthetic append\n")) {
					t.Fatalf("%s broke append epoch/revision continuity: %#v -> %#v", stage, previous, source)
				}
				previous = source
			}
			if err := os.Mkdir(filepath.Join(root, "sessions"), 0o700); err != nil {
				t.Fatal(err)
			}
			check("sibling mkdir")
			if err := os.Remove(filepath.Join(root, "sessions")); err != nil {
				t.Fatal(err)
			}
			check("sibling removal")
			physical := filepath.Join(c.Home, "unrelated-claude")
			if err := os.MkdirAll(physical, 0o700); err != nil {
				t.Fatal(err)
			}
			loaded.RootAliases = []capturedaemon.RootAlias{{Agent: "claude", Root: filepath.Join(c.Home, ".claude"), Path: physical}}
			check("unrelated alias enabled")
			loaded.RootAliases = nil
			check("unrelated alias disabled")
		})
	}
}

func TestAdversarialAliasExternalOptOutMismatch(t *testing.T) {
	c, _ := runtimeFixture(t)
	logical := filepath.Join(c.Home, ".claude")
	old := filepath.Join(filepath.Dir(c.Home), "old-agent")
	current := filepath.Join(filepath.Dir(c.Home), "current-agent")
	const uuid = "11111111-1111-1111-1111-111111111111"
	oldMain := filepath.Join(old, "projects", "zzz", uuid+".jsonl")
	runtimeWrite(t, oldMain)
	original, ok := adapters.NewClaude(c.Home).Classify(oldMain, old)
	if !ok {
		t.Fatal("old main unclassified")
	}
	// Old physical spelling disappears, then is reused with unrelated stale project
	// content. Declarations, not the first UUID found on that route, define identity.
	if err := os.RemoveAll(old); err != nil {
		t.Fatal(err)
	}
	runtimeWrite(t, filepath.Join(old, "projects", "aaa", uuid+".jsonl"))
	runtimeWrite(t, filepath.Join(current, "projects", "zzz", uuid+".jsonl"))
	artifact := filepath.Join(current, "tasks", uuid, "result.txt")
	runtimeWrite(t, artifact)
	c.RootAliases = []capturedaemon.RootAlias{{Agent: "claude", Root: logical, Path: old}, {Agent: "claude", Root: logical, Path: current}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	r, err := newRuntimeComposition(c)
	if err != nil {
		t.Fatal(err)
	}
	mainReq, err := capturesource.Classify(t.Context(), r.sourceOptions(""), "claude", filepath.Join(current, "projects", "zzz", uuid+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	q, err := sessioncapture.Open(t.Context(), c.Spool, r.queueOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := q.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err := q.RecordOwnership(t.Context(), mainReq); err != nil {
		t.Fatal(err)
	}
	accepted, err := capturesource.Classify(t.Context(), r.sourceOptions(""), "claude", artifact)
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Identity != mainReq.Identity {
		t.Fatal("external artifact did not retain full durable owner")
	}
	if _, err := q.Capture(t.Context(), accepted); err != nil {
		t.Fatal(err)
	}
	if _, err := exclusion.RecordFile(c.PolicyDir, original); err != nil {
		t.Fatal(err)
	}
	backend := publication.NewLocal(c.Store)
	if err := publication.WriteArchive(t.Context(), backend, publication.Archive{Contract: publication.Contract, ArchiveID: c.ArchiveID}); err != nil {
		t.Fatal(err)
	}
	if err := q.Deliver(t.Context(), backend, accepted.Identity); !errors.Is(err, sessioncapture.ErrSessionExcluded) {
		t.Fatalf("queued artifact delivered despite old physical opt-out: %v", err)
	}
	if _, _, err := backend.Current(t.Context(), accepted.Identity.CurrentKey()); !errors.Is(err, publication.ErrNotFound) {
		t.Fatalf("excluded artifact published current: %v", err)
	}
	registry, err := exclusion.Load(c.PolicyDir)
	if err != nil {
		t.Fatal(err)
	}
	// This is the validated logical owner that aliasPrivacyOwner obtains from the
	// durable index. Test aliasExcluded in isolation; do not fake a durable queue.
	owner := adapters.SessionFile{Agent: "claude", Root: logical, Path: filepath.Join(logical, "tasks", uuid, "result.txt"), SourcePath: artifact, SessionKey: "zzz/" + uuid}
	req := sessioncapture.Request{Identity: publication.Identity{ArchiveID: c.ArchiveID, MachineID: c.MachineID, Agent: "claude"}, Path: artifact, Role: "artifact"}
	denied, err := r.aliasExcluded(req, owner, registry)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("aliasExcluded helper denied=%v", denied)
	req.Identity.SessionID = mainReq.Identity.SessionID
	if err := r.policy(false)(t.Context(), req); !errors.Is(err, sessioncapture.ErrSessionExcluded) {
		t.Fatalf("ACTUAL CALLBACK bypass or unexpected error: %v", err)
	}
	t.Log("actual capture/delivery callback correctly denies via claimed-session guard")
	req.Identity.SessionID = ""
	if err := r.policy(true)(t.Context(), req); !errors.Is(err, sessioncapture.ErrSessionExcluded) {
		t.Fatalf("discovery metadata policy mismatch: %v", err)
	}
	if !denied {
		t.Fatal("validated durable owner lost old-root opt-out")
	}
}

func runtimeAppendSynthetic(t *testing.T, path string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := f.WriteString("synthetic append\n")
	closeErr := f.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		t.Fatal(err)
	}
}

func TestAdversarialPiCustomLogResourceIdentity(t *testing.T) {
	c, config := runtimeFixture(t)
	root := filepath.Join(c.Home, "custom-pi-agent")
	path := filepath.Join(root, "pi-debug.log")
	runtimeWrite(t, path)
	if err := os.MkdirAll(filepath.Join(root, "sessions", "--project--"), 0o700); err != nil {
		t.Fatal(err)
	}
	c.DefaultRoots = false
	c.Roots = []capturedaemon.Root{{Agent: "pi", Path: root}}
	if err := capturedaemon.Save(config, c); err != nil {
		t.Fatal(err)
	}
	loaded, err := capturedaemon.Load(config)
	if err != nil {
		t.Fatal(err)
	}
	r, err := newRuntimeComposition(loaded)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(root))
	namespace := hex.EncodeToString(sum[:])
	key := "roots/" + namespace[:10] + "/pi-debug.log"
	identity := publication.Identity{ArchiveID: c.ArchiveID, MachineID: c.MachineID, Agent: "pi", SessionID: "resource/" + namespace + "/" + key}
	for _, stage := range []string{"sessions present", "sessions removed"} {
		if stage == "sessions removed" {
			if err := os.RemoveAll(filepath.Join(root, "sessions")); err != nil {
				t.Fatal(err)
			}
		}
		req, err := capturesource.Classify(t.Context(), r.sourceOptions(config), "pi", path)
		if err != nil {
			t.Fatal(err)
		}
		if req.SourceID != key || req.Identity != identity || req.Role != "artifact" || !slices.Contains(req.Associations, "adapter-role:log") || !slices.Contains(req.Associations, "ownership:agent-resource") {
			t.Fatalf("%s changed explicit log resource: %#v", stage, req)
		}
	}
}
