package syncer

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cespare/xxhash/v2"
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

// Resume inputs (including corrupt state) must not change the result. The current
// implementation ignores them and hashes the full content; this is not a benchmark
// or proof of saved-prefix resume optimization.
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
			if len(c.data) >= len(before) {
				want.tail = xxhash.Sum64String(c.data[len(before):])
				want.prefixNewline = c.data[len(before)-1] == '\n'
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

func TestHashContentReportsReadErrors(t *testing.T) {
	_, err := hashContent(failingReader{}, 10, nil, nil)
	if err == nil {
		t.Error("no error")
	}
}

type failingReader struct{}

func (failingReader) ReadAt([]byte, int64) (int, error) { return 0, bytes.ErrTooLarge }

// TestCompressChecksTheBytesItHashed rewrites a file between hashing and compressing it, which
// must fail the upload rather than send bytes that don't match the recorded sha256. A segment
// is read again from where the last upload ended, so a change there must be caught too.
func TestCompressChecksTheBytesItHashed(t *testing.T) {
	defer func(old int64) { largeFileSize = old }(largeFileSize)
	const before, after, added = "line one\nline two\n", "line ONE\nline two\n", "line three\n"
	prev := &state.Upload{SHA256: raw.SHA256([]byte(before)), Size: int64(len(before))}
	for _, large := range []int64{1 << 20, 4} {
		for _, segment := range []bool{false, true} {
			for _, rewrite := range []bool{false, true} {
				t.Run(fmt.Sprintf("large=%d/segment=%v/rewrite=%v", large, segment, rewrite), func(t *testing.T) {
					largeFileSize = large
					text, changed, last := before, after, (*state.Upload)(nil)
					if segment {
						text, changed, last = before+added, before+strings.ToUpper(added), prev
					}
					path := filepath.Join(t.TempDir(), "s.jsonl")
					if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
						t.Fatal(err)
					}
					st, err := os.Stat(path)
					if err != nil {
						t.Fatal(err)
					}
					s := &Syncer{tmpDir: t.TempDir()}
					c, err := s.streamedContent(path, st, last, nil, false)
					if err != nil {
						t.Fatal(err)
					}
					if rewrite {
						if err := os.WriteFile(path, []byte(changed), 0o600); err != nil {
							t.Fatal(err)
						}
					}
					piece := raw.Piece{End: c.size}
					if segment {
						piece.Start = prev.Size
					}
					u := &upload{file: state.File{SHA256: c.sha, Size: c.size}, piece: piece, resume: c.resume, tail: c.tail}
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
					if want := raw.SHA256([]byte(text[piece.Start:])); u.piece.SHA256 != want {
						t.Errorf("piece sha256 %s, want %s", u.piece.SHA256, want)
					}
				})
			}
		}
	}
}
