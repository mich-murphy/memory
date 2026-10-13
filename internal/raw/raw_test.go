package raw

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

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

func TestDecompressRejectsGarbage(t *testing.T) {
	if _, err := Decompress([]byte("not zstd")); err == nil {
		t.Error("no error")
	}
}
