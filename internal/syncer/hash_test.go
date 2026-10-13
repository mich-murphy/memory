package syncer

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mich-murphy/memory/internal/raw"
	"github.com/mich-murphy/memory/internal/state"
)

func hashOf(t *testing.T, data string, prev *state.Upload, saved *state.Resume) hashed {
	t.Helper()
	h, err := hashContent(strings.NewReader(data), int64(len(data)), prev, saved)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestHashContentMatchesAFullReadWithOrWithoutAResume(t *testing.T) {
	defer func(old int64) { chunkSize = old }(chunkSize)
	chunkSize = 7 // reads cross the old size mid-chunk
	before := "line one\nline two\n"
	first := hashOf(t, before, nil, nil)
	prev := &state.Upload{SHA256: first.sha, Size: first.size}
	corrupt := *first.resume
	corrupt.SHA = []byte("not a sha256 state")
	cases := []struct {
		name, data   string
		saved        *state.Resume
		wantAppended bool
	}{
		{"append, resumed", before + "line three\n", first.resume, true},
		{"append, no resume", before + "line three\n", nil, true},
		{"append, unreadable resume", before + "line three\n", &corrupt, true},
		{"unchanged, resumed", before, first.resume, true},
		{"rewrite, resumed", "line ONE\nline two\nline three\n", first.resume, false},
		{"rewrite, no resume", "line ONE\nline two\nline three\n", nil, false},
		{"shrunk", "line one\n", first.resume, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := hashOf(t, c.data, prev, c.saved)
			want := hashed{
				sha: raw.SHA256([]byte(c.data)), size: int64(len(c.data)), lines: int64(strings.Count(c.data, "\n")),
				appended: c.wantAppended,
			}
			if diff := cmp.Diff(want, got, cmp.AllowUnexported(hashed{}), cmp.FilterPath(func(p cmp.Path) bool {
				return p.Last().String() == ".resume"
			}, cmp.Ignore())); diff != "" {
				t.Errorf("(-want +got):\n%s", diff)
			}
			again := hashOf(t, c.data+"more\n", &state.Upload{SHA256: got.sha, Size: got.size}, got.resume)
			if !again.appended || again.sha != raw.SHA256([]byte(c.data+"more\n")) {
				t.Errorf("the resume this left doesn't carry on: %+v", again)
			}
		})
	}
}

// TestHashContentUsesTheResume checks the resume is what an append is hashed from: a resume
// for other bytes with the same xxhash gives a different SHA-256 than a full read.
func TestHashContentUsesTheResume(t *testing.T) {
	a := hashOf(t, "aaaa\n", nil, nil)
	b := hashOf(t, "bbbb\n", nil, nil)
	forged := &state.Resume{Size: a.size, Fast: a.resume.Fast, SHA: b.resume.SHA}
	got := hashOf(t, "aaaa\nmore\n", &state.Upload{SHA256: a.sha, Size: a.size}, forged)
	if got.sha != raw.SHA256([]byte("bbbb\nmore\n")) {
		t.Errorf("sha256 %s wasn't carried on from the resume", got.sha)
	}
}

func TestHashContentReportsReadErrors(t *testing.T) {
	_, err := hashContent(failingReader{}, 10, nil, nil)
	if err == nil {
		t.Error("no error")
	}
}

type failingReader struct{}

func (failingReader) ReadAt([]byte, int64) (int, error) { return 0, bytes.ErrTooLarge }

// TestCompressChecksTheBytesItHashed rewrites a file between hashing and compressing it, which
// must fail the upload rather than send bytes that don't match the recorded sha256.
func TestCompressChecksTheBytesItHashed(t *testing.T) {
	defer func(old int64) { largeFileSize = old }(largeFileSize)
	const before, after = "line one\nline two\n", "line ONE\nline two\n"
	for _, large := range []int64{1 << 20, 4} {
		for _, rewrite := range []bool{false, true} {
			t.Run(fmt.Sprintf("large=%d/rewrite=%v", large, rewrite), func(t *testing.T) {
				largeFileSize = large
				path := filepath.Join(t.TempDir(), "s.jsonl")
				if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
					t.Fatal(err)
				}
				st, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				s := &Syncer{tmpDir: t.TempDir()}
				c, err := s.streamedContent(path, st, nil, nil, false)
				if err != nil {
					t.Fatal(err)
				}
				if rewrite {
					if err := os.WriteFile(path, []byte(after), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				u := &upload{file: state.File{SHA256: c.sha, Size: c.size}, piece: raw.Piece{End: c.size}, resume: c.resume}
				defer u.cleanup()
				err = c.compress(u)
				if rewrite {
					if !errors.Is(err, errRewritten) {
						t.Errorf("compress after a rewrite = %v, want errRewritten", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if u.piece.SHA256 != raw.SHA256([]byte(before)) {
					t.Errorf("piece sha256 %s, want the file's", u.piece.SHA256)
				}
			})
		}
	}
}
