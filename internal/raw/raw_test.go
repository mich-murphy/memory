package raw

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"testing/iotest"

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
	h := sha256.New()
	if err := CompressStream(bytes.NewReader(append(slices.Clone(data), "trailing"...)), &out, int64(len(data)), h); err != nil {
		t.Fatal(err)
	}
	if sha := hex.EncodeToString(h.Sum(nil)); sha != SHA256(data) {
		t.Fatalf("sha256 %s, want %s", sha, SHA256(data))
	}
	return out.Bytes()
}

func TestCompressStreamFailsWhenItCantReadEveryByte(t *testing.T) {
	failing := func() io.Reader {
		return io.MultiReader(strings.NewReader(strings.Repeat("x", 500)), iotest.ErrReader(errors.New("i/o error")))
	}
	for _, c := range []struct {
		name   string
		r      io.Reader
		length int64
	}{
		{"shrank, small", strings.NewReader("short"), 100},
		{"shrank, streamed", strings.NewReader("short"), 1000},
		{"read error, streamed", failing(), 1000},
	} {
		var out bytes.Buffer
		if err := CompressStream(c.r, &out, c.length, sha256.New()); err == nil {
			t.Errorf("%s: no error", c.name)
		}
	}
}
