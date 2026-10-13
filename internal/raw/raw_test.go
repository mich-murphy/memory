package raw

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

var errMissing = errors.New("missing")

type memStore map[string][]byte

func (m memStore) Get(_ context.Context, key string) ([]byte, error) {
	if b, ok := m[key]; ok {
		return b, nil
	}
	return nil, errMissing
}

func (m memStore) List(_ context.Context, prefix string) ([]string, error) {
	var out []string
	for k := range m {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	return out, nil
}

func FuzzCompressRoundTrip(f *testing.F) {
	f.Add([]byte(""))
	f.Add([]byte("{\"type\":\"user\"}\n"))
	f.Add(bytes.Repeat([]byte("abc"), 10000))
	f.Fuzz(func(t *testing.T, data []byte) {
		for name, body := range map[string][]byte{"Compress": Compress(data), "CompressStream": stream(t, data)} {
			got, err := Decompress(body)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if !bytes.Equal(got, data) {
				t.Fatalf("%s round trip changed %d bytes into %d", name, len(data), len(got))
			}
			var h zstd.Header
			if err := h.Decode(body); err != nil || !h.HasFCS || h.FrameContentSize != uint64(len(data)) {
				t.Fatalf("%s frame doesn't record its content size: %+v, %v", name, h, err)
			}
		}
	})
}

func stream(t *testing.T, data []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	sha, err := CompressStream(bytes.NewReader(append(slices.Clone(data), "trailing"...)), &out, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if sha != SHA256(data) {
		t.Fatalf("sha256 %s, want %s", sha, SHA256(data))
	}
	return out.Bytes()
}

func TestCompressStreamNoticesAShrunkFile(t *testing.T) {
	for _, length := range []int64{100, 1000} {
		var out bytes.Buffer
		if _, err := CompressStream(strings.NewReader("short"), &out, length); err == nil {
			t.Errorf("length %d: no error for a file that shrank", length)
		}
	}
}

func TestReadKeyJoinsSegmentsAndSkipsStaleOnes(t *testing.T) {
	key := "raw/agent=pi/machine=m/s.jsonl.zst"
	s := memStore{
		key:                       Compress([]byte("abc")),
		SegmentKey(key, 3, 5):     Compress([]byte("de")),
		SegmentKey(key, 5, 6):     Compress([]byte("f")),
		SegmentKey(key, 1, 2):     Compress([]byte("X")), // left behind by a fold
		SegmentKey("other", 3, 4): Compress([]byte("Y")),
	}
	got, err := ReadKey(t.Context(), s, key)
	if err != nil || string(got) != "abcdef" {
		t.Errorf("ReadKey = %q, %v", got, err)
	}
	if _, err := ReadKey(t.Context(), s, "nope"); !errors.Is(err, errMissing) {
		t.Errorf("missing key: %v", err)
	}
	for _, name := range []string{"junk.zst", "x-6.zst"} {
		bad := maps.Clone(s)
		bad[key+".d/"+name] = Compress(nil)
		if _, err := ReadKey(t.Context(), bad, key); !errors.Is(err, errSegmentName) {
			t.Errorf("segment named %s: %v", name, err)
		}
	}
	corrupt := maps.Clone(s)
	corrupt[SegmentKey(key, 6, 7)] = []byte("not zstd")
	if _, err := ReadKey(t.Context(), corrupt, key); err == nil || !strings.Contains(err.Error(), SegmentKey(key, 6, 7)) {
		t.Errorf("corrupt segment: %v, want an error naming it", err)
	}
}

func TestDecompressRejectsGarbage(t *testing.T) {
	if _, err := Decompress([]byte("not zstd")); err == nil {
		t.Error("no error")
	}
}
