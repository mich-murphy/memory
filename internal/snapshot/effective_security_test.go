package snapshot

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/capturepolicy"
	"github.com/mich-murphy/memory/internal/exclusion"
	"github.com/mich-murphy/memory/internal/raw"
	"github.com/mich-murphy/memory/internal/testenv"
)

func effectiveFixture(t *testing.T) (*testenv.Env, Options) {
	t.Helper()
	e := testenv.New(t)
	p, err := capturepolicy.New("", "")
	if err != nil {
		t.Fatal(err)
	}
	return e, Options{WorkingDirectory: privateScratch(t), Policy: p, Home: e.Home, Authorize: func(_ context.Context, a AssetEvidence) (AssetEvidence, error) { return a, nil }}
}

func effectiveBuild(t *testing.T, e *testenv.Env, o Options, cwd string) *Snapshot {
	t.Helper()
	s, err := BuildWithOptions(t.Context(), adapters.NewClaude(e.Home), e.Claude, cwd, "", o)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func gitFixture(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git fixture: %v %s", err, out)
	}
}

func TestEffectiveAliasImportPluginAndSelectors(t *testing.T) {
	e, o := effectiveFixture(t)
	imported := filepath.Join(e.Home, "outside.md")
	testenv.Write(t, imported, "outside instruction")
	testenv.Write(t, filepath.Join(e.Claude, "CLAUDE.md"), "@"+imported+"\n")
	alias := filepath.Join(e.Claude, "rules", "alias.md")
	symlink(t, imported, alias)
	plugin := filepath.Join(e.Home, "installed-plugin")
	testenv.Write(t, filepath.Join(plugin, "skill.md"), "plugin instructions")
	manifest := filepath.Join(e.Claude, "plugins", "installed_plugins.json")
	testenv.Write(t, manifest, `{"plugins":{"demo":[{"installPath":"`+plugin+`"}]}}`)
	s := effectiveBuild(t, e, o, "")
	kinds := map[string]bool{}
	foundAlias := false
	for _, a := range s.Evidence {
		kinds[a.InputKind] = true
		if a.OriginalPath == alias {
			foundAlias = len(a.Identities) > 1 && strings.HasPrefix(a.Selector, "pattern:user:")
		}
		if a.Scope == "import" && a.OriginalPath != imported {
			t.Fatal("wrong import original")
		}
	}
	if !foundAlias || !kinds["plugin-manifest"] {
		t.Fatal("missing trusted provenance")
	}
	o.Authorize = func(_ context.Context, a AssetEvidence) (AssetEvidence, error) { a.Selector = "forged"; return a, nil }
	denied := effectiveBuild(t, e, o, "")
	if denied.Complete || len(denied.Harness.Files) != 0 {
		t.Fatal("forged callback accepted")
	}
}

func TestEffectiveGitNestedSelectionAndDeniedDependency(t *testing.T) {
	e, o := effectiveFixture(t)
	gitFixture(t, e.Project, "init")
	testenv.Write(t, filepath.Join(e.Project, ".gitignore"), "vendor/\n")
	testenv.Write(t, filepath.Join(e.Project, "nested", "CLAUDE.md"), "selected")
	testenv.Write(t, filepath.Join(e.Project, "vendor", "CLAUDE.md"), "not selected")
	s := effectiveBuild(t, e, o, e.Project)
	for _, f := range s.Harness.Files {
		if strings.Contains(f.Path, "vendor/") {
			t.Fatal("ignored directory selected")
		}
	}
	seen := false
	for _, a := range s.Evidence {
		if a.InputKind == "git-selection" {
			seen = true
		}
	}
	if !seen {
		t.Fatal("missing git authorization")
	}
	marker := filepath.Join(e.Project, "hook-ran")
	testenv.Write(t, filepath.Join(e.Project, ".git", "config"), "[include]\n path = /not/authorized\n[core]\n fsmonitor = touch "+marker+"\n")
	effectiveBuild(t, e, o, e.Project)
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("git executed config")
	}
	o.Authorize = func(_ context.Context, a AssetEvidence) (AssetEvidence, error) {
		if a.InputKind == "git-selection" && filepath.Base(a.OriginalPath) == "index" {
			return a, errors.New("deny index")
		}
		return a, nil
	}
	testenv.Write(t, filepath.Join(e.Project, ".git", "index"), "unauthorized invalid index")
	s = effectiveBuild(t, e, o, e.Project)
	if s.Complete {
		t.Fatal("denied git selection green")
	}
}

func TestEffectiveSnapshotMtimeEquivalence(t *testing.T) {
	e, o := effectiveFixture(t)
	p := filepath.Join(e.Claude, "CLAUDE.md")
	testenv.Write(t, p, "stable")
	first := effectiveBuild(t, e, o, "")
	b1, err := Bundle(t.Context(), first)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b1.Reader.Close() }()
	if err := os.Chtimes(p, time.Unix(100, 0), time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
	second := effectiveBuild(t, e, o, "")
	b2, err := Bundle(t.Context(), second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b2.Reader.Close() }()
	if b1.SHA256 != b2.SHA256 || first.Harness.ID != second.Harness.ID {
		t.Fatal("mtime manufactured new content")
	}
	if first.Mtimes["harness:user:CLAUDE.md"] == second.Mtimes["harness:user:CLAUDE.md"] {
		t.Fatal("fixture failed to change observation time")
	}
}

func TestMalformedBundleMembers(t *testing.T) {
	for _, tc := range []struct {
		name string
		typ  byte
	}{{"../escape", tar.TypeReg}, {"assets/../../escape", tar.TypeReg}, {"snapshot.json", tar.TypeSymlink}, {"harness.json", tar.TypeDir}} {
		t.Run(tc.name, func(t *testing.T) {
			var b bytes.Buffer
			tw := tar.NewWriter(&b)
			if err := tw.WriteHeader(&tar.Header{Name: tc.name, Mode: 0o600, Typeflag: tc.typ}); err != nil {
				t.Fatal(err)
			}
			if err := tw.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadBundle(t.Context(), &b); err == nil {
				t.Fatal("malformed TAR accepted")
			}
		})
	}
	e, o := effectiveFixture(t)
	s := effectiveBuild(t, e, o, "")
	b, err := Bundle(t.Context(), s)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(b.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Reader.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadBundle(t.Context(), bytes.NewReader(append(data, []byte("trailing")...))); err == nil {
		t.Fatal("trailing data accepted")
	}
}

func TestEffectivePiPackageGitAndDeniedInputs(t *testing.T) {
	e, o := effectiveFixture(t)
	o.UserBase = filepath.Join(e.Home, ".pi", "agent")
	repo := filepath.Join(o.UserBase, "git", "example", "package")
	testenv.Write(t, filepath.Join(repo, "extension.ts"), "export default {}")
	gitFixture(t, repo, "init")
	gitFixture(t, repo, "add", "extension.ts")
	gitFixture(t, repo, "-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid", "commit", "-m", "fixture")
	buildPi := func() *Snapshot {
		t.Helper()
		s, err := BuildWithOptions(t.Context(), adapters.NewPi(e.Home), o.UserBase, "", "", o)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	s := buildPi()
	found := false
	for _, f := range s.Harness.Files {
		if f.Path == "pi-package-provenance.json" {
			found = true
			if !bytes.Contains(s.Blobs[f.SHA256], []byte(`"dirty":null`)) {
				t.Fatal("wrong revision")
			}
		}
	}
	if !found {
		t.Fatal("package provenance missing", s.Harness.Skipped)
	}
	o.Authorize = func(_ context.Context, a AssetEvidence) (AssetEvidence, error) {
		if strings.HasSuffix(a.OriginalPath, "extension.ts") {
			return a, errors.New("denied package input")
		}
		return a, nil
	}
	if buildPi().Complete {
		t.Fatal("denied Git dependency falsely complete")
	}
	o.Authorize = func(_ context.Context, a AssetEvidence) (AssetEvidence, error) { return a, nil }
	symlink(t, filepath.Join(repo, "extension.ts"), filepath.Join(repo, "alias.ts"))
	if buildPi().Complete {
		t.Fatal("unbounded package alias accepted")
	}
}

func TestEffectiveMetadataAndReaderBudgets(t *testing.T) {
	e, o := effectiveFixture(t)
	state := &buildState{ctx: t.Context(), options: &o}
	c := initCollected("claude", []*buildState{state})
	c.budget.evidence = maxEvidenceBytes
	if err := c.allow("/selected", []string{"/selected"}, "user", "pattern:user:root:CLAUDE.md", "file"); err == nil {
		t.Fatal("evidence limit ignored")
	}
	c.budget.evidence = 0
	o.Authorize = func(_ context.Context, a AssetEvidence) (AssetEvidence, error) {
		a.Identities[0] = "forged"
		return a, nil
	}
	if err := c.allow("/selected", []string{"/selected"}, "user", "pattern:user:root:CLAUDE.md", "file"); err == nil {
		t.Fatal("identity mutation accepted")
	}
	o.Authorize = func(_ context.Context, a AssetEvidence) (AssetEvidence, error) { return a, nil }
	s := effectiveBuild(t, e, o, "")
	s.Harness.Files = append(s.Harness.Files, File{Path: strings.Repeat("x", maxBundleMetadata)})
	if _, err := Bundle(t.Context(), s); err == nil {
		t.Fatal("metadata budget ignored")
	}
	w := &bundleCapacityWriter{writer: io.Discard, remaining: 1}
	if _, err := w.Write([]byte("xx")); err == nil {
		t.Fatal("serialization cap ignored")
	}
	r := &bundleReader{ctx: t.Context(), body: bytes.NewReader([]byte("body"))}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Read(make([]byte, 1)); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	r = &bundleReader{ctx: ctx, body: bytes.NewReader([]byte("body"))}
	cancel()
	if _, err := r.Read(make([]byte, 1)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestEffectiveAuthorizationCannotSwapLeafToCredential(t *testing.T) {
	e, o := effectiveFixture(t)
	p := filepath.Join(e.Claude, "CLAUDE.md")
	testenv.Write(t, p, "safe")
	credential := filepath.Join(e.Home, "auth.json")
	testenv.Write(t, credential, "private-test-marker")
	o.Authorize = func(_ context.Context, a AssetEvidence) (AssetEvidence, error) {
		if a.OriginalPath == p {
			if err := os.Remove(p); err != nil {
				return a, err
			}
			if err := os.Symlink(credential, p); err != nil {
				return a, err
			}
		}
		return a, nil
	}
	s := effectiveBuild(t, e, o, "")
	if s.Complete {
		t.Fatal("swapped leaf green")
	}
	for _, body := range s.Blobs {
		if bytes.Contains(body, []byte("private-test-marker")) {
			t.Fatal("unauthorized leaf read")
		}
	}
}

func TestEffectiveSHA256Package(t *testing.T) {
	e, o := effectiveFixture(t)
	o.UserBase = filepath.Join(e.Home, ".pi", "agent")
	repo := filepath.Join(o.UserBase, "git", "sha256-package")
	testenv.Write(t, filepath.Join(repo, "extension.ts"), "safe")
	gitFixture(t, repo, "init", "--object-format=sha256")
	gitFixture(t, repo, "add", "extension.ts")
	gitFixture(t, repo, "-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid", "commit", "-m", "fixture")
	s, err := BuildWithOptions(t.Context(), adapters.NewPi(e.Home), o.UserBase, "", "", o)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range s.Harness.Files {
		if f.Path == "pi-package-provenance.json" {
			found = true
		}
	}
	if !found {
		t.Fatal("SHA256 package lost", s.Harness.Skipped)
	}
}

func TestPassiveGitOwnsAuthorizedClosureBeforeObservation(t *testing.T) {
	e, _ := effectiveFixture(t)
	repo := e.Project
	testenv.Write(t, filepath.Join(repo, "selected.md"), "fixture bytes")
	gitFixture(t, repo, "init")
	gitFixture(t, repo, "add", "selected.md")
	gitFixture(t, repo, "-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid", "commit", "-m", "fixture")
	authorized := map[string]bool{}
	dir, err := adapters.SnapshotRepository(t.Context(), repo, func(p string) error { authorized[p] = true; return nil }, privateScratch(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	}()
	if authorized[filepath.Join(repo, "selected.md")] {
		t.Fatal("unselected body authorized")
	}
	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	out, err := adapters.SnapshotRepositoryGit(t.Context(), repo, dir, 1024, "ls-files", "--cached")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "selected.md\n") {
		t.Fatalf("owned selection changed: %q", out)
	}
}

func TestEffectiveAggregateExclusionWithholdsSharedMemory(t *testing.T) {
	e, o := effectiveFixture(t)
	testenv.Write(t, filepath.Join(e.Claude, "CLAUDE.md"), "shared instructions")
	testenv.Write(t, filepath.Join(e.Claude, "agent-memory", "private.md"), "shared memory")
	o.Exclusions = exclusion.Registry{{Agent: "claude", Root: e.Claude, ResolvedRoot: e.Claude, SessionKey: "other-session"}}
	s := effectiveBuild(t, e, o, "")
	if s.Complete || s.Memory == nil || s.Memory.Complete {
		t.Fatal("aggregate exclusion falsely complete")
	}
	for _, e := range s.Evidence {
		if e.Scope == "user" {
			t.Fatal("shared originals authorized despite aggregate exclusion")
		}
	}
	for _, b := range s.Blobs {
		if bytes.Contains(b, []byte("shared memory")) || bytes.Contains(b, []byte("shared instructions")) {
			t.Fatal("shared excluded bytes selected")
		}
	}
}

func privateScratch(t *testing.T) string {
	t.Helper()
	p := t.TempDir()
	if err := os.Chmod(p, 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestEffectiveExcludedUnrelatedCredentialDoesNotEraseSelection(t *testing.T) {
	e, o := effectiveFixture(t)
	gitFixture(t, e.Project, "init")
	safe := filepath.Join(e.Project, "nested", "AGENTS.md")
	testenv.Write(t, safe, "safe instructions")
	for _, p := range []string{"tracked.env", "untracked.env", "ignored/private.env"} {
		testenv.Write(t, filepath.Join(e.Project, p), "private unrelated body")
	}
	testenv.Write(t, filepath.Join(e.Project, ".gitignore"), "ignored/\n")
	gitFixture(t, e.Project, "add", "tracked.env", "nested/AGENTS.md")
	o.Authorize = func(_ context.Context, a AssetEvidence) (AssetEvidence, error) {
		if strings.HasSuffix(a.OriginalPath, ".env") {
			t.Fatal("unselected credential authorized")
		}
		return a, nil
	}
	s := effectiveBuild(t, e, o, e.Project)
	found := false
	for _, f := range s.Harness.Files {
		if f.SHA256 == raw.SHA256([]byte("safe instructions")) {
			found = true
		}
		if bytes.Contains(s.Blobs[f.SHA256], []byte("private unrelated body")) {
			t.Fatal("private body selected")
		}
	}
	if !found {
		t.Fatal("safe selected instruction lost", s.Harness.Skipped)
	}
	entries, err := os.ReadDir(o.WorkingDirectory)
	if err != nil || len(entries) != 0 {
		t.Fatal("scratch not cleaned", err)
	}
}

func TestEffectiveAuthorizationAncestorSwapNeverReadsOutside(t *testing.T) {
	e, o := effectiveFixture(t)
	sub := filepath.Join(e.Claude, "rules", "sub")
	p := filepath.Join(sub, "safe.md")
	outside := filepath.Join(e.Home, "outside")
	testenv.Write(t, p, "safe")
	testenv.Write(t, filepath.Join(outside, "safe.md"), "OUTSIDE-SELECTED-ASSET-UNIQUE")
	swapped := false
	o.Authorize = func(_ context.Context, a AssetEvidence) (AssetEvidence, error) {
		if a.OriginalPath == p && !swapped {
			swapped = true
			if err := os.Rename(sub, sub+"-old"); err != nil {
				return a, err
			}
			if err := os.Symlink(outside, sub); err != nil {
				return a, err
			}
		}
		return a, nil
	}
	s := effectiveBuild(t, e, o, "")
	if !swapped || s.Complete {
		t.Fatal("swap not rejected")
	}
	for _, b := range s.Blobs {
		if bytes.Contains(b, []byte("OUTSIDE-SELECTED-ASSET-UNIQUE")) {
			t.Fatal("unauthorized body read")
		}
	}
}

func TestEffectiveDeniedIgnorePrunesOnlyAffectedSubtree(t *testing.T) {
	e, o := effectiveFixture(t)
	gitFixture(t, e.Project, "init")
	testenv.Write(t, filepath.Join(e.Project, "safe", "AGENTS.md"), "safe instructions retained")
	testenv.Write(t, filepath.Join(e.Project, "unrelated", ".gitignore"), "nested/\n")
	testenv.Write(t, filepath.Join(e.Project, "unrelated", "nested", "AGENTS.md"), "must not overselect")
	// Indexed paths must also be pruned; absence of filename placeholders alone is insufficient.
	gitFixture(t, e.Project, "add", "-f", "safe/AGENTS.md", "unrelated/nested/AGENTS.md")
	o.Authorize = func(_ context.Context, a AssetEvidence) (AssetEvidence, error) {
		if a.OriginalPath == filepath.Join(e.Project, "unrelated", ".gitignore") {
			return a, errors.New("denied rule")
		}
		return a, nil
	}
	s := effectiveBuild(t, e, o, e.Project)
	safe, partial := false, false
	for _, f := range s.Harness.Files {
		if f.Path == "safe/AGENTS.md" {
			safe = true
		}
		if strings.HasPrefix(f.Path, "unrelated/") {
			t.Fatal("denied rules treated as empty")
		}
	}
	for _, skip := range s.Harness.Skipped {
		if skip.Reason == "git_subtree_pruned" {
			partial = true
		}
	}
	if !safe || !partial || s.Complete {
		t.Fatal("healthy selection lost or false completeness", s.Harness.Skipped)
	}
}

func TestEffectiveUnreadableRuleAndDeniedDirectoryRemainPruned(t *testing.T) {
	for _, mode := range []string{"disappearing-rule", "denied-directory"} {
		t.Run(mode, func(t *testing.T) {
			e, o := effectiveFixture(t)
			gitFixture(t, e.Project, "init")
			sub := filepath.Join(e.Project, "unrelated")
			rule := filepath.Join(sub, ".gitignore")
			testenv.Write(t, rule, "nested/\n")
			testenv.Write(t, filepath.Join(sub, "nested", "AGENTS.md"), "must not select")
			testenv.Write(t, filepath.Join(e.Project, "safe", "AGENTS.md"), "kept")
			gitFixture(t, e.Project, "add", "-f", "unrelated/nested/AGENTS.md", "safe/AGENTS.md")
			o.Authorize = func(_ context.Context, a AssetEvidence) (AssetEvidence, error) {
				if mode == "disappearing-rule" && a.OriginalPath == rule {
					return a, os.Remove(rule)
				}
				if mode == "denied-directory" && a.OriginalPath == sub {
					return a, errors.New("deny subtree")
				}
				return a, nil
			}
			s := effectiveBuild(t, e, o, e.Project)
			safe := false
			for _, f := range s.Harness.Files {
				if strings.HasPrefix(f.Path, "unrelated/") {
					t.Fatal("unsafe subtree selected")
				}
				if f.Path == "safe/AGENTS.md" {
					safe = true
				}
			}
			if !safe || s.Complete {
				t.Fatal("safe asset lost or partial missing", s.Harness.Skipped)
			}
		})
	}
}

func TestEffectiveSafeIgnoreAliasRetainsAllIdentities(t *testing.T) {
	e, o := effectiveFixture(t)
	gitFixture(t, e.Project, "init")
	rules := filepath.Join(e.Home, "safe-ignore-rules")
	testenv.Write(t, rules, "vendor/\n")
	rule := filepath.Join(e.Project, ".gitignore")
	symlink(t, rules, rule)
	testenv.Write(t, filepath.Join(e.Project, "safe", "AGENTS.md"), "kept")
	testenv.Write(t, filepath.Join(e.Project, "vendor", "AGENTS.md"), "not selected")
	seen := false
	o.Authorize = func(_ context.Context, a AssetEvidence) (AssetEvidence, error) {
		if a.OriginalPath == rule {
			hasOriginal, hasResolved := false, false
			for _, id := range a.Identities {
				hasOriginal = hasOriginal || id == rule
				hasResolved = hasResolved || id == realPath(rules)
			}
			seen = hasOriginal && hasResolved
		}
		return a, nil
	}
	s := effectiveBuild(t, e, o, e.Project)
	safe := false
	for _, f := range s.Harness.Files {
		if f.Path == "safe/AGENTS.md" {
			safe = true
		}
		if f.Path == "vendor/AGENTS.md" {
			t.Fatal("alias rules ignored")
		}
	}
	if !safe || !seen {
		t.Fatal("declared safe alias was rejected", s.Harness.Skipped)
	}
}
