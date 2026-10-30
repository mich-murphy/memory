package snapshot

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/capturepolicy"
	"github.com/mich-murphy/memory/internal/testenv"
)

func TestEffectiveOptionsBundleClosure(t *testing.T) {
	e := testenv.New(t)
	testenv.Write(t, filepath.Join(e.Claude, "CLAUDE.md"), "same safe bytes")
	testenv.Write(t, filepath.Join(e.Claude, "rules", "rule.md"), "same safe bytes")
	testenv.Write(t, filepath.Join(e.Home, ".claude.json"), `{"primaryApiKey":"fake-secret","mcpServers":{}}`)
	policy, err := capturepolicy.New("", "")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	opts := Options{Policy: policy, Home: e.Home, Authorize: func(_ context.Context, a AssetEvidence) (AssetEvidence, error) {
		seen[a.OriginalPath] = true
		return a, nil
	}}
	s, err := BuildWithOptions(t.Context(), adapters.NewClaude(e.Home), e.Claude, "", "", opts)
	if err != nil {
		t.Fatal(err)
	}
	if !seen[filepath.Join(e.Home, ".claude.json")] || len(s.Evidence) == 0 {
		t.Fatal("missing generated input evidence")
	}
	first, err := Bundle(t.Context(), s)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(first.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Reader.Close(); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("fake-secret")) {
		t.Fatal("raw credential reached bundle")
	}
	if err := os.RemoveAll(e.Claude); err != nil {
		t.Fatal(err)
	}
	restored, err := ReadBundle(t.Context(), bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Blobs) != len(s.Blobs) {
		t.Fatal("closure missing")
	}
	s.Mtimes["volatile"] = 12345
	second, err := Bundle(t.Context(), s)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Reader.Close(); err != nil {
		t.Fatal(err)
	}
	if first.SHA256 != second.SHA256 {
		t.Fatal("volatile times changed content")
	}
	delete(s.Blobs, s.Harness.Files[0].SHA256)
	if _, err := Bundle(t.Context(), s); err == nil {
		t.Fatal("missing closure accepted")
	}
}

func TestEffectiveAuthorizationAndCancellation(t *testing.T) {
	e := testenv.New(t)
	p := filepath.Join(e.Claude, "CLAUDE.md")
	testenv.Write(t, p, "unreadable after authorization")
	policy, err := capturepolicy.New("", "")
	if err != nil {
		t.Fatal(err)
	}
	opts := Options{Policy: policy, Home: e.Home, Authorize: func(_ context.Context, a AssetEvidence) (AssetEvidence, error) {
		if a.OriginalPath == p {
			return a, errors.New("denied")
		}
		return a, nil
	}}
	s, err := BuildWithOptions(t.Context(), adapters.NewClaude(e.Home), e.Claude, "", "", opts)
	if err != nil {
		t.Fatal(err)
	}
	if s.Complete {
		t.Fatal("denial falsely complete")
	}
	for _, f := range s.Harness.Files {
		if f.Path == "CLAUDE.md" {
			t.Fatal("denied input selected")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := BuildWithOptions(ctx, adapters.NewClaude(e.Home), e.Claude, "", "", opts); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := ReadBundle(t.Context(), bytes.NewReader([]byte("invalid"))); err == nil {
		t.Fatal("invalid bundle accepted")
	}
	if _, err := BuildWithOptions(t.Context(), adapters.NewPi(e.Home), filepath.Join(e.Home, ".pi", "agent"), "", "", opts); err == nil {
		t.Fatal("implicit Pi environment accepted")
	}
}
