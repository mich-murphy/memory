package capturebackend

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mich-murphy/memory/internal/publication"
	"github.com/mich-murphy/memory/internal/publicationr2"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

func spec() Spec {
	return Spec{Kind: "r2", Endpoint: "https://" + strings.Repeat("a", 32) + ".r2.cloudflarestorage.com", Bucket: "offline-bucket", AccessKeyEnv: "SYNTHETIC_ACCESS", SecretKeyEnv: "SYNTHETIC_SECRET"}
}

func identity() publication.Identity {
	return publication.Identity{ArchiveID: "archive", MachineID: "machine", Agent: "pi", SessionID: "session"}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func deps(rt http.RoundTripper) Dependencies {
	return Dependencies{LookupEnv: func(n string) (string, bool) { return "synthetic-private-" + n, true }, HTTPClient: &http.Client{Transport: rt}}
}

func TestValidationAndCancellationAreOffline(t *testing.T) {
	cases := []Spec{{}, {Kind: "unknown"}, {Kind: "local", LocalDir: "relative"}, {Kind: "local", LocalDir: "/"}, {Kind: "local", LocalDir: "/safe", Bucket: "unused"}}
	for _, endpoint := range []string{"http://" + strings.TrimPrefix(spec().Endpoint, "https://"), spec().Endpoint + "/", spec().Endpoint + "?", spec().Endpoint + "#", spec().Endpoint + ":443", "https://private@" + strings.TrimPrefix(spec().Endpoint, "https://"), "https://evil.invalid", spec().Endpoint + "/path"} {
		s := spec()
		s.Endpoint = endpoint
		cases = append(cases, s)
	}
	for _, change := range []func(*Spec){func(s *Spec) { s.Bucket = "Bad" }, func(s *Spec) { s.AccessKeyEnv = "bad-name" }, func(s *Spec) { s.SecretKeyEnv = s.AccessKeyEnv }, func(s *Spec) { s.LocalDir = "/unused" }} {
		s := spec()
		change(&s)
		cases = append(cases, s)
	}
	d := Dependencies{LookupEnv: func(string) (string, bool) { t.Fatal("invalid config looked up credentials"); return "", false }}
	for _, s := range cases {
		if s.Validate() == nil {
			t.Fatalf("accepted invalid spec: kind=%s", s.Kind)
		}
		if _, err := Open(t.Context(), s, d); err == nil {
			t.Fatal("opened invalid spec")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Open(ctx, spec(), d); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	local := Spec{Kind: "local", LocalDir: filepath.Join(t.TempDir(), "not-created")}
	if err := ValidateIdentity(local, publication.Identity{}); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(t.Context(), local, d); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(local.LocalDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("local factory created destination")
	}
}

func TestCredentialErrorsAndDefaults(t *testing.T) {
	for _, missing := range []string{spec().AccessKeyEnv, spec().SecretKeyEnv} {
		d := Dependencies{LookupEnv: func(n string) (string, bool) { return "synthetic-credential", n != missing }}
		if _, err := Open(t.Context(), spec(), d); err == nil || strings.Contains(err.Error(), missing) {
			t.Fatal("unsafe/missing credential error")
		}
	}
	t.Setenv(spec().AccessKeyEnv, "synthetic-access")
	t.Setenv(spec().SecretKeyEnv, "synthetic-secret")
	if _, err := Open(t.Context(), spec(), Dependencies{}); err != nil {
		t.Fatal(err)
	}
	c := defaultHTTPClient()
	tr := c.Transport.(*http.Transport)
	if c.Timeout <= 0 || tr.DialContext == nil || tr.MaxConnsPerHost <= 0 || tr.ResponseHeaderTimeout <= 0 || tr.TLSHandshakeTimeout <= 0 || tr.Proxy != nil {
		t.Fatal("unbounded transport")
	}
	if !errors.Is(c.CheckRedirect(nil, nil), http.ErrUseLastResponse) {
		t.Fatal("redirect enabled")
	}
	ctx, cancel := context.WithCancel(t.Context())
	d := Dependencies{LookupEnv: func(string) (string, bool) { cancel(); return "synthetic", true }}
	if _, err := Open(ctx, spec(), d); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestCancellationDuringSecretLookup(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	d := Dependencies{LookupEnv: func(n string) (string, bool) {
		calls++
		if n == spec().SecretKeyEnv {
			cancel()
		}
		return "synthetic", true
	}}
	if _, err := Open(ctx, spec(), d); !errors.Is(err, context.Canceled) || calls != 2 {
		t.Fatal("secret lookup cancellation ignored")
	}
}

func TestIdentityLimitCountsUTF8Hex(t *testing.T) {
	id := identity()
	overhead := len(id.BaseKey()) - len(id.SessionID)*2 + len("/manifests/") + 64 + len(".json")
	id.SessionID = strings.Repeat("a", (1024-overhead)/2)
	if err := ValidateIdentity(spec(), id); err != nil {
		t.Fatal(err)
	}
	id.SessionID += "é"
	if err := ValidateIdentity(spec(), id); err == nil {
		t.Fatal("accepted oversized encoded key")
	}
	for _, v := range []string{"", string([]byte{255}), "bad\n"} {
		id.SessionID = v
		if ValidateIdentity(spec(), id) == nil {
			t.Fatal("invalid identity")
		}
	}
	if ValidateIdentity(Spec{}, id) == nil {
		t.Fatal("invalid spec")
	}
}

type fixture struct {
	t      *testing.T
	bodies map[string][]byte
	tokens map[string]string
	calls  int
	lost   bool
	deny   bool
}

func (f *fixture) RoundTrip(r *http.Request) (*http.Response, error) {
	f.t.Helper()
	f.calls++
	if r.URL.Scheme != "https" || r.URL.Host != strings.TrimPrefix(spec().Endpoint, "https://") || (r.URL.RawQuery != "x-id=GetObject" && r.URL.RawQuery != "x-id=PutObject") {
		f.t.Fatal("unexpected endpoint/query")
	}
	auth := r.Header.Get("Authorization")
	if !strings.Contains(auth, "/auto/s3/aws4_request") || !strings.Contains(auth, "synthetic-private-SYNTHETIC_ACCESS") {
		f.t.Fatal("not explicit static R2 signing")
	}
	w := httptest.NewRecorder()
	key := strings.TrimPrefix(r.URL.Path, "/offline-bucket/")
	fail := func(code int) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(code)
		_, _ = w.WriteString("<Error><Code>AccessDenied</Code><Message>PRIVATE_BODY</Message></Error>")
	}
	if f.deny {
		fail(403)
		return w.Result(), nil
	}
	switch r.Method {
	case http.MethodGet:
		b, ok := f.bodies[key]
		if !ok {
			fail(404)
			break
		}
		w.Header().Set("ETag", f.tokens[key])
		w.Header().Set("Content-Length", strconv.Itoa(len(b)))
		_, _ = w.Write(b)
	case http.MethodPut:
		old, exists := f.tokens[key]
		none, match := r.Header.Get("If-None-Match"), r.Header.Get("If-Match")
		if none != "*" && match == "" {
			f.t.Fatal("unguarded PUT")
		}
		if (none == "*" && exists) || (match != "" && match != old) {
			fail(412)
			break
		}
		b, err := io.ReadAll(r.Body)
		if err != nil {
			f.t.Fatal(err)
		}
		f.bodies[key] = b
		f.tokens[key] = fmt.Sprintf(`"opaque-not-md5-%d"`, f.calls)
		w.Header().Set("ETag", f.tokens[key])
		if f.lost && strings.HasSuffix(key, "current.json") {
			f.lost = false
			return nil, errors.New("PRIVATE_BODY lost ack")
		}
	default:
		f.t.Fatal("HEAD/list/delete/multipart not permitted")
	}
	return w.Result(), nil
}

func newFixture(t *testing.T) *fixture {
	return &fixture{t: t, bodies: map[string][]byte{}, tokens: map[string]string{}}
}

func factory(t *testing.T, rt http.RoundTripper) publication.Backend {
	t.Helper()
	b, err := Open(t.Context(), spec(), deps(rt))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestFactoryConditionalOpaqueAndSafeErrors(t *testing.T) {
	f := newFixture(t)
	b := factory(t, f)
	key := identity().CurrentKey()
	if err := b.CompareAndSwap(t.Context(), key, "", []byte("first")); err != nil {
		t.Fatal(err)
	}
	_, tok, err := b.Current(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	if err = b.CompareAndSwap(t.Context(), key, tok, []byte("second")); err != nil {
		t.Fatal(err)
	}
	if err = b.CompareAndSwap(t.Context(), key, tok, []byte("stale")); !errors.Is(err, publication.ErrConflict) {
		t.Fatal(err)
	}
	f.deny = true
	n := f.calls
	if _, _, err = b.Current(t.Context(), key); !errors.Is(err, publicationr2.ErrDestinationPaused) || strings.Contains(err.Error(), "PRIVATE") {
		t.Fatal(err)
	}
	if f.calls != n+1 {
		t.Fatal("SDK retried")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	n = f.calls
	if _, _, err = b.Current(ctx, key); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if f.calls != n {
		t.Fatal("canceled request sent")
	}
	b = factory(t, roundTrip(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("PRIVATE_BODY synthetic-private-secret " + spec().Endpoint)
	}))
	if _, _, err = b.Current(t.Context(), key); err == nil || strings.Contains(err.Error(), "PRIVATE") || strings.Contains(err.Error(), "cloudflare") || strings.Contains(err.Error(), "synthetic") {
		t.Fatal("unsafe transport diagnostic")
	}
}

func TestFactoryQueueReaderExactBytesLostAck(t *testing.T) {
	f := newFixture(t)
	f.lost = true
	b := factory(t, f)
	if err := publication.WriteArchive(t.Context(), b, publication.Archive{Contract: publication.Contract, ArchiveID: identity().ArchiveID}); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	path := filepath.Join(root, "source")
	raw := []byte{'h', 'i', '\n', 0, 255}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	q, err := sessioncapture.Open(t.Context(), filepath.Join(root, "queue"), sessioncapture.Options{ArchiveID: identity().ArchiveID, MaxSpoolBytes: 1 << 20, CheckPolicy: func(context.Context, sessioncapture.Request) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := q.Close(); err != nil {
			t.Error(err)
		}
	}()
	req := sessioncapture.Request{Identity: identity(), SourceID: "source", Role: "transcript", Path: path, ObservedAtNS: 1}
	m, err := q.Capture(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if err = q.Deliver(t.Context(), b, identity()); err != nil {
		t.Fatal(err)
	}
	if f.lost {
		t.Fatal("lost ack not reached")
	}
	dest := filepath.Join(root, "export")
	out, err := publication.ExportSession(t.Context(), b, identity(), dest)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dest, out.Bodies[0].Path))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, raw) || out.Manifest.PublicationID != m.PublicationID {
		t.Fatal("reader bytes or identity changed")
	}
	f.deny = true
	if err = q.Deliver(t.Context(), b, identity()); err != nil {
		t.Fatal(err)
	} // already acknowledged, no work
}
