package publicationr2

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mich-murphy/memory/internal/publication"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

func TestSDKWriterCurrentHeadsPinnedExportWithoutSpool(t *testing.T) {
	p := newProtocol()
	lists := 0
	bad := publication.Identity{ArchiveID: "archive", MachineID: "machine", Agent: "claude", SessionID: "a"}
	var fault atomic.Int32
	peer := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("list-type") != "2" {
			if r.Method == http.MethodGet && strings.TrimPrefix(r.URL.Path, "/offline-bucket/") == bad.CurrentKey() {
				switch fault.Load() {
				case 1:
					w.Header().Set("ETag", `"corrupt"`)
					_, _ = io.WriteString(w, `{"contract":"unsupported"}`)
					return
				case 2:
					protocolError(w, http.StatusServiceUnavailable, "private503")
					return
				case 3:
					protocolError(w, http.StatusForbidden, "private403")
					return
				}
			}
			p.ServeHTTP(w, r)
			return
		}
		lists++
		q := r.URL.Query()
		prefix := q.Get("prefix")
		after := q.Get("start-after")
		limit, err := strconv.Atoi(q.Get("max-keys"))
		if err != nil || limit < 1 || limit > 256 || q.Get("delimiter") != "/" || q.Get("continuation-token") != "" || !strings.HasPrefix(prefix, "capture-v1/archives/") || !strings.HasSuffix(prefix, "/sessions/") {
			t.Error("unscoped list")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		p.mu.Lock()
		seen := map[string]bool{}
		for key := range p.objects {
			if rest, ok := strings.CutPrefix(key, prefix); ok {
				if at := strings.Index(rest, "/"); at >= 0 {
					ns := prefix + rest[:at+1]
					if ns > after {
						seen[ns] = true
					}
				}
			}
		}
		p.mu.Unlock()
		prefixes := make([]string, 0, len(seen))
		for ns := range seen {
			prefixes = append(prefixes, ns)
		}
		slices.Sort(prefixes)
		truncated := len(prefixes) > limit
		if truncated {
			prefixes = prefixes[:limit]
		}
		_, _ = fmt.Fprintf(w, "<ListBucketResult><IsTruncated>%t</IsTruncated>", truncated)
		for _, ns := range prefixes {
			_, _ = fmt.Fprint(w, "<CommonPrefixes><Prefix>")
			_ = xml.EscapeText(w, []byte(ns))
			_, _ = fmt.Fprint(w, "</Prefix></CommonPrefixes>")
		}
		_, _ = fmt.Fprint(w, "</ListBucketResult>")
	})
	b := backendFor(t, peer, 5*time.Second)
	if err := publication.WriteArchive(t.Context(), b, publication.Archive{Contract: publication.Contract, ArchiveID: "archive"}); err != nil {
		t.Fatal(err)
	}
	spool := filepath.Join(t.TempDir(), "spool")
	q, err := sessioncapture.Open(t.Context(), spool, sessioncapture.Options{ArchiveID: "archive", MaxSpoolBytes: 1 << 20, CheckPolicy: func(context.Context, sessioncapture.Request) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte("synthetic exact source\n")
	source := filepath.Join(t.TempDir(), "source.jsonl")
	if err = os.WriteFile(source, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	expected := map[publication.Identity]publication.Manifest{}
	for _, agent := range []string{"claude", "codex", "pi"} {
		for _, session := range []string{"a", "b"} {
			id := publication.Identity{ArchiveID: "archive", MachineID: "machine", Agent: agent, SessionID: session}
			m, e := q.Capture(t.Context(), sessioncapture.Request{Identity: id, SourceID: "source", Path: source, Role: "main"})
			if e != nil {
				t.Fatal(e)
			}
			if e = q.Deliver(t.Context(), b, id); e != nil {
				t.Fatal(e)
			}
			expected[id] = m
		}
	}
	if err = q.Close(); err != nil {
		t.Fatal(err)
	}
	if err = errors.Join(os.RemoveAll(spool), os.Remove(source)); err != nil {
		t.Fatal(err)
	}
	// An uploaded precommit orphan is listed but must not become a head.
	orphan := identity
	orphan.Agent = "claude"
	orphan.SessionID = "orphan"
	p.mu.Lock()
	p.objects[orphan.BaseKey()+"/objects/"+strings.Repeat("a", 64)+".zst"] = stored{body: []byte("orphan"), etag: `"orphan"`}
	p.mu.Unlock()
	cursor := ""
	found := map[publication.Identity]bool{}
	for range 10 {
		page, e := publication.ReadCurrentHeads(t.Context(), b, "archive", "machine", cursor, 1)
		if e != nil {
			t.Fatal(e)
		}
		for _, head := range page.Heads {
			m, ok := expected[head.Identity]
			if !ok || found[head.Identity] || head.Sequence != m.Sequence || head.PublicationID != m.PublicationID {
				t.Fatal("wrong head", head)
			}
			found[head.Identity] = true
			dest := filepath.Join(t.TempDir(), "export")
			out, e := publication.ExportSession(t.Context(), b, head.Identity, dest)
			if e != nil {
				t.Fatal(e)
			}
			if out.Manifest.Identity != head.Identity || out.Manifest.Sequence != head.Sequence || out.Manifest.PublicationID != head.PublicationID || len(out.Bodies) != 1 || len(out.Manifest.Sources) != 1 || !reflect.DeepEqual(out.Manifest.Sources, m.Sources) {
				t.Fatal("pinned coordinates or source closure differs", out)
			}
			if out.Bodies[0].SourceID != m.Sources[0].SourceID || out.Bodies[0].EpochID != m.Sources[0].EpochID || out.Bodies[0].SHA256 != m.Sources[0].Object.SHA256 || out.Bodies[0].Size != int64(len(raw)) {
				t.Fatal("body coordinates differ")
			}
			body, e := os.ReadFile(filepath.Join(dest, out.Bodies[0].Path))
			if e != nil || !bytes.Equal(body, raw) {
				t.Fatal("export differs", e)
			}
		}
		if page.Complete {
			break
		}
		if page.NextCursor == cursor {
			t.Fatal("nonadvancing cursor")
		}
		cursor = page.NextCursor
	}
	if len(found) != 6 || lists != 7 {
		t.Fatalf("found %d lists %d", len(found), lists)
	}
	for _, mode := range []int32{1, 2, 3} {
		fault.Store(mode)
		page, e := publication.ReadCurrentHeads(t.Context(), b, "archive", "machine", "", 2)
		if mode == 3 {
			if !errors.Is(e, publication.ErrDestinationPaused) || !errors.Is(e, ErrDestinationPaused) {
				t.Fatal("SDK auth did not fail endpoint", e)
			}
			continue
		}
		if e != nil || len(page.Heads) != 1 || len(page.Failures) != 1 || page.Failures[0] != (publication.PublicationHeadFailure{Identity: bad, Reason: "current-unreadable"}) || page.Heads[0].Identity.SessionID != "b" {
			t.Fatal("SDK broken A starved B", page, e)
		}
		healthy := page.Heads[0]
		m := expected[healthy.Identity]
		dest := filepath.Join(t.TempDir(), "healthy-export")
		out, e := publication.ExportSession(t.Context(), b, healthy.Identity, dest)
		if e != nil {
			t.Fatal(e)
		}
		if out.Manifest.Identity != healthy.Identity || out.Manifest.Sequence != healthy.Sequence || out.Manifest.PublicationID != healthy.PublicationID || len(out.Bodies) != 1 || len(out.Manifest.Sources) != 1 || !reflect.DeepEqual(out.Manifest.Sources, m.Sources) {
			t.Fatal("healthy pinned source closure differs")
		}
		body, e := os.ReadFile(filepath.Join(dest, out.Bodies[0].Path))
		if e != nil || !bytes.Equal(body, raw) {
			t.Fatal("healthy export differs", e)
		}
		next, e := publication.ReadCurrentHeads(t.Context(), b, "archive", "machine", page.NextCursor, 2)
		if e != nil || len(next.Heads) != 0 || len(next.Failures) != 0 || next.NextCursor == page.NextCursor {
			t.Fatal("failure prevented later-page advance", next, e)
		}
	}
	fault.Store(0)
	repaired, e := publication.ReadCurrentHeads(t.Context(), b, "archive", "machine", "", 2)
	if e != nil || len(repaired.Heads) != 2 || len(repaired.Failures) != 0 {
		t.Fatal("full sweep did not retry repaired A", repaired, e)
	}
}

type listHTTPFunc func(*http.Request) (*http.Response, error)

func (f listHTTPFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

type infiniteListBody struct{ read, closed int }

func (b *infiniteListBody) Read(p []byte) (int, error) {
	for n := range p {
		p[n] = 'x'
	}
	b.read += len(p)
	return len(p), nil
}
func (b *infiniteListBody) Close() error { b.closed++; return nil }
func TestListResponseBoundBeforeSDK(t *testing.T) {
	body := &infiniteListBody{}
	client := boundedListClient{client: listHTTPFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
	})}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://synthetic.invalid", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(req)
	if response != nil {
		if closeErr := response.Body.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
	}
	if err == nil || body.read != MaxListResponseBytes+1 || body.closed != 1 {
		t.Fatal("unbounded successful XML response", body, err)
	}
}

func TestListRejectsMalformedAndNonadvancingNamespaces(t *testing.T) {
	prefix, _ := publication.SessionPrefix(identity)
	for _, body := range []string{
		"<ListBucketResult><IsTruncated>true</IsTruncated></ListBucketResult>",
		"<ListBucketResult><Contents><Key>history</Key></Contents></ListBucketResult>",
		"<ListBucketResult><CommonPrefixes><Prefix>outside/</Prefix></CommonPrefixes></ListBucketResult>",
		"<ListBucketResult><CommonPrefixes><Prefix>" + prefix + "FF/</Prefix></CommonPrefixes></ListBucketResult>",
		"<ListBucketResult><CommonPrefixes><Prefix>" + identity.BaseKey() + "/</Prefix></CommonPrefixes></ListBucketResult>",
	} {
		b := backendFor(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }), time.Second)
		if _, _, err := b.ListSessionNamespaces(t.Context(), identity, identity.BaseKey()+"/", 1); err == nil {
			t.Fatal("invalid LIST accepted", body)
		}
	}
	for _, status := range []int{403, 404, 500} {
		b := backendFor(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { protocolError(w, status, "private") }), time.Second)
		if _, _, err := b.ListSessionNamespaces(t.Context(), identity, "", 256); err == nil || strings.Contains(err.Error(), "private") {
			t.Fatal("unsafe list error", err)
		}
	}
}
