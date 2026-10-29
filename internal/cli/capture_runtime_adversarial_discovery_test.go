package cli

import (
	"errors"
	"os"
	"path/filepath"
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
			if _, err := q.Capture(t.Context(), before); err != nil {
				t.Fatal(err)
			}
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
