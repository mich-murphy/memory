package cli

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/mich-murphy/memory/internal/capturepolicy"
	"github.com/mich-murphy/memory/internal/exclusion"
	"github.com/mich-murphy/memory/internal/publication"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

const (
	isolationArchive  = "isolation-fixture-archive"
	isolationFirst    = "{\"type\":\"user\",\"uuid\":\"isolation-u1\",\"message\":{\"role\":\"user\",\"content\":\"Synthetic isolation first\"}}\n"
	isolationLatest   = isolationFirst + "{\"type\":\"assistant\",\"uuid\":\"isolation-a1\",\"message\":{\"role\":\"assistant\",\"content\":\"Synthetic isolation latest\"}}\n"
	isolationRevoked  = isolationLatest + "{\"type\":\"user\",\"uuid\":\"isolation-u2\",\"message\":{\"role\":\"user\",\"content\":\"Synthetic isolation after revocation\"}}\n"
	isolationArtifact = "Synthetic opaque artifact, never an event.\n"
)

type isolationExpectations struct {
	Identity     publication.Identity `json:"identity"`
	MainBody     string               `json:"main_body"`
	RevokedBody  string               `json:"revoked_body"`
	ArtifactBody string               `json:"artifact_body"`
	Artifact     publication.Source   `json:"artifact"`
	Main         publication.Source   `json:"main"`
}

// runCaptureIsolationFixture is an explicit process fixture, never a daemon or
// host discovery entry point. Successful crash intentionally bypasses Close.
func runCaptureIsolationFixture(ctx context.Context, args []string, o IO) (int, error) {
	f := flag.NewFlagSet("isolation-fixture", flag.ContinueOnError)
	f.SetOutput(o.Stderr)
	root := f.String("output", "", "new owned fixture root (existing for repair/revoke)")
	stage := f.String("stage", "", "crash, repair, or revoke")
	if err := f.Parse(args); err != nil {
		return exitUsage, err
	}
	if *root == "" || f.NArg() != 0 || (*stage != "crash" && *stage != "repair" && *stage != "revoke") {
		return exitUsage, errors.New("require --output ROOT --stage crash|repair|revoke")
	}
	absolute, err := filepath.Abs(*root)
	if err != nil {
		return exitFailed, err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(absolute))
	if err != nil {
		return exitFailed, err
	}
	absolute = filepath.Join(parent, filepath.Base(absolute))
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if *stage == "crash" {
		if err := writeFixtureCoverageMetadata(os.Getenv("E2E_GOCOVERDIR")); err != nil {
			return exitFailed, err
		}
	}
	if err := isolationStage(ctx, absolute, *stage); err != nil {
		return exitFailed, err
	}
	return exitOK, nil
}

func isolationStage(ctx context.Context, root, stage string) (resultErr error) {
	id := publication.Identity{ArchiveID: isolationArchive, MachineID: "synthetic-machine", Agent: "claude", SessionID: "isolation-session"}
	if stage == "crash" {
		if err := os.Mkdir(root, 0o700); err != nil {
			return errors.New("crash requires a new fixture root")
		}
		if err := makeFixtureDirs(filepath.Join(root, "policy"), filepath.Join(root, "sources")); err != nil {
			return err
		}
		if err := isolationWrite(root, "policy/owned", []byte("synthetic-isolation-v1")); err != nil {
			return err
		}
	}
	if err := isolationOwned(root); err != nil {
		return err
	}
	backend := publication.NewLocal(filepath.Join(root, "store"))
	if stage == "crash" {
		if err := publication.WriteArchive(ctx, backend, publication.Archive{Contract: publication.Contract, ArchiveID: isolationArchive}); err != nil {
			return err
		}
	}
	q, err := sessioncapture.Open(ctx, filepath.Join(root, "spool"), sessioncapture.Options{ArchiveID: isolationArchive, MaxSpoolBytes: 16 << 20, CheckPolicy: isolationPolicy(root)})
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, q.Close()) }()
	if stage == "crash" {
		return isolationCrash(ctx, root, q, backend, id)
	}
	return isolationContinue(ctx, root, stage, q, backend, id)
}

func isolationOwned(root string) error {
	// No host-derived roots or symlink aliases are authorized by this fixture.
	for p := root; ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return sessioncapture.ErrPolicy
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	b, err := os.ReadFile(filepath.Join(root, "policy", "owned"))
	if err != nil {
		return err
	}
	if string(b) != "synthetic-isolation-v1" {
		return sessioncapture.ErrPolicy
	}
	return nil
}

func isolationPolicy(root string) func(context.Context, sessioncapture.Request) error {
	return func(ctx context.Context, r sessioncapture.Request) error {
		if err := isolationOwned(root); err != nil {
			return err
		}
		// Only these two synthetic originals qualify for retained-payload delivery.
		if r.Path != filepath.Join(root, "sources", "main.jsonl") && r.Path != filepath.Join(root, "sources", "artifact.bin") {
			return sessioncapture.ErrPolicy
		}
		if r.Optional {
			_, err := os.Lstat(filepath.Join(root, "policy", "deny-optional"))
			if err == nil {
				return sessioncapture.ErrPolicy
			}
			if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		return isolationEffectivePolicy(ctx, root, r)
	}
}

func isolationEffectivePolicy(ctx context.Context, root string, r sessioncapture.Request) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	registry, err := exclusion.Load(filepath.Join(root, "policy"))
	if err != nil {
		return err
	}
	if captureSessionExcluded(registry, r.Identity) {
		return sessioncapture.ErrSessionExcluded
	}
	resolved, err := capturepolicy.Resolve(r.Path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	policy, err := capturepolicy.New(filepath.Join(root, "policy", "config.env"), filepath.Join(root, "spool"))
	if err != nil {
		return err
	}
	if policy.Excludes(resolved.Identities) {
		return sessioncapture.ErrPolicy
	}
	for _, p := range resolved.Identities {
		if p != r.Path {
			return sessioncapture.ErrPolicy
		}
		for _, record := range registry {
			if slices.Contains(record.Paths, p) {
				return sessioncapture.ErrPolicy
			}
		}
	}
	return nil
}

func isolationWrite(root, name string, body []byte) error {
	path := filepath.Join(root, name)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(body)
	err = errors.Join(err, f.Sync(), f.Close())
	if err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}

func isolationCapture(ctx context.Context, root string, q *sessioncapture.Queue, id publication.Identity, optional bool, body string) (publication.Manifest, error) {
	name, role := "main.jsonl", "main"
	if optional {
		name, role = "artifact.bin", "artifact"
	}
	if err := isolationWrite(root, "sources/"+name, []byte(body)); err != nil {
		return publication.Manifest{}, err
	}
	return q.Capture(ctx, sessioncapture.Request{Identity: id, SourceID: role, Role: role, Path: filepath.Join(root, "sources", name), Optional: optional})
}
