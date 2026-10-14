package capturepolicy

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
)

func writeSynthetic(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("synthetic-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func linkSynthetic(t *testing.T, target, path string) {
	t.Helper()
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
}

func TestResolveRetainsRelativeAndAbsoluteIntermediateNames(t *testing.T) {
	dir := t.TempDir()
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	writeSynthetic(t, filepath.Join(dir, "vault-entry"))
	linkSynthetic(t, "vault-entry", filepath.Join(dir, "auth.json"))
	linkSynthetic(t, filepath.Join(dir, "auth.json"), filepath.Join(dir, "skill.md"))
	resolution, err := Resolve(filepath.Join(dir, "skill.md"))
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Path != filepath.Join(realDir, "vault-entry") {
		t.Errorf("Path = %q", resolution.Path)
	}
	if !slices.Contains(resolution.Identities, filepath.Join(realDir, "auth.json")) {
		t.Errorf("lost credential identity: %q", resolution.Identities)
	}
	policy, err := New("", "")
	if err != nil {
		t.Fatal(err)
	}
	if !policy.Excludes(resolution.Identities) {
		t.Errorf("credential chain not excluded: %+v", resolution)
	}
	t.Chdir(dir)
	relative, err := Resolve("./skill.md")
	if err != nil {
		t.Fatal(err)
	}
	if relative.Path != resolution.Path || !policy.Excludes(relative.Identities) {
		t.Errorf("relative resolution = %+v", relative)
	}
}

func TestResolveRetainsAncestorAliasesAndStateBeforeExternalLeaf(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	writeSynthetic(t, filepath.Join(state, "nested", "unused"))
	outside := filepath.Join(dir, "outside.md")
	writeSynthetic(t, outside)
	linkSynthetic(t, outside, filepath.Join(state, "nested", "launch.md"))
	linkSynthetic(t, "state", filepath.Join(dir, "state-alias"))
	linkSynthetic(t, "state-alias", filepath.Join(dir, "second-alias"))
	policy, err := New("", filepath.Join(dir, "state-alias"))
	if err != nil {
		t.Fatal(err)
	}
	resolution, err := Resolve(filepath.Join(dir, "second-alias", "nested", "launch.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !policy.Excludes(resolution.Identities) {
		t.Errorf("intermediate state path not excluded: %+v", resolution)
	}
	outsideResolution, err := Resolve(outside)
	if err != nil {
		t.Fatal(err)
	}
	if policy.Excludes(outsideResolution.Identities) {
		t.Errorf("unrelated target alone was excluded")
	}
}

func TestResolveDotDotFollowsSymlinkBeforeBackingUp(t *testing.T) {
	dir := t.TempDir()
	writeSynthetic(t, filepath.Join(dir, "real", "child", "unused"))
	writeSynthetic(t, filepath.Join(dir, "real", "notes"))
	linkSynthetic(t, filepath.Join(dir, "real", "child"), filepath.Join(dir, "alias"))
	// filepath.Join would incorrectly erase alias/.. before the resolver sees the symlink.
	path := filepath.Join(dir, "alias") + string(filepath.Separator) + ".." + string(filepath.Separator) + "notes"
	resolution, err := Resolve(path)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Path != want {
		t.Errorf("Resolve = %q, want %q", resolution.Path, want)
	}
	if root, err := Resolve(string(filepath.Separator)); err != nil || root.Path != string(filepath.Separator) {
		t.Errorf("Resolve root = %+v, %v", root, err)
	}
}

func TestCollectorConfigAllIdentitiesAndBackupsAreProtected(t *testing.T) {
	dir := t.TempDir()
	writeSynthetic(t, filepath.Join(dir, "innocent"))
	linkSynthetic(t, "innocent", filepath.Join(dir, "middle-login"))
	linkSynthetic(t, "middle-login", filepath.Join(dir, "custom-login"))
	linkSynthetic(t, "middle-login", filepath.Join(dir, "skill.md"))
	policy, err := New(filepath.Join(dir, "custom-login"), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"custom-login", "middle-login", "innocent", "skill.md", "custom-login.bak.old~", "middle-login.bak~", "innocent.orig"} {
		path := filepath.Join(dir, name)
		if BaseName(name) != name {
			writeSynthetic(t, path)
		}
		resolution, err := Resolve(path)
		if err != nil {
			t.Fatal(err)
		}
		if !policy.Excludes(resolution.Identities) {
			t.Errorf("collector identity %q not excluded: %+v", name, resolution)
		}
	}
	missing := filepath.Join(dir, "missing-config")
	policy, err = New(missing, filepath.Join(dir, "missing-state"))
	if err != nil {
		t.Fatal(err)
	}
	if !policy.Excludes([]string{missing + ".bak.old"}) {
		t.Errorf("missing collector config backup not excluded")
	}
}

func TestResolveErrorsFailClosedAndRetainIntermediateIdentity(t *testing.T) {
	dir := t.TempDir()
	linkSynthetic(t, "b", filepath.Join(dir, "a"))
	linkSynthetic(t, "a", filepath.Join(dir, "b"))
	if result, err := Resolve(filepath.Join(dir, "a")); err == nil || result.Path != "" || len(result.Identities) < 2 {
		t.Errorf("cyclic Resolve = %+v, %v", result, err)
	}
	if _, err := New(filepath.Join(dir, "a"), ""); err == nil {
		t.Error("cyclic collector config accepted")
	}
	if _, err := New("", filepath.Join(dir, "a")); err == nil {
		t.Error("cyclic collector state accepted")
	}
	result, err := Resolve(filepath.Join(dir, "missing"))
	if !errors.Is(err, fs.ErrNotExist) || len(result.Identities) == 0 {
		t.Errorf("missing Resolve = %+v, %v", result, err)
	}
	writeSynthetic(t, filepath.Join(dir, "file"))
	if _, err := Resolve(filepath.Join(dir, "file", "child")); err == nil {
		t.Error("non-directory ancestor accepted")
	}
	for i := range maxSymlinks + 1 {
		linkSynthetic(t, "chain"+strconv.Itoa(i+1), filepath.Join(dir, "chain"+strconv.Itoa(i)))
	}
	if _, err := Resolve(filepath.Join(dir, "chain0")); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Errorf("excessive chain = %v, want bounded-chain error", err)
	}
}
