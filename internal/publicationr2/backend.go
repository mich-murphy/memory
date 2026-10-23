// Package publicationr2 supplies an SDK-backed session-publication/v2 transport.
// Live R2 conditional-write semantics remain UNVERIFIED; see README.md.
package publicationr2

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/mich-murphy/memory/internal/publication"
)

// MaxObjectSize bounds single-PutObject source payloads (no multipart uploads).
const MaxObjectSize int64 = 1 << 30

// ErrDestinationPaused identifies destination authentication/authorization failures.
var ErrDestinationPaused = errors.New("publication destination paused")

// Backend uses only the supplied client; it never discovers credentials or config.
type Backend struct {
	client  *s3.Client
	bucket  string
	timeout time.Duration
}

var _ publication.Backend = (*Backend)(nil)

// New requires an explicit client, bucket, and positive operation budget.
func New(client *s3.Client, bucket string, timeout time.Duration) (*Backend, error) {
	if client == nil || timeout <= 0 || bucket == "" || strings.TrimSpace(bucket) != bucket || strings.ContainsAny(bucket, "/\\\x00\r\n") {
		return nil, errors.New("invalid publication destination options")
	}
	return &Backend{client: client, bucket: bucket, timeout: timeout}, nil
}

type keyKind int

const (
	invalidKey keyKind = iota
	referenceKey
	manifestKey
	objectKey
)

func canonicalHex(s string, length int) bool {
	if s == "" || (length > 0 && len(s) != length) || strings.ToLower(s) != s {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// keyType accepts only canonical writer keys, never path-cleaned aliases.
func keyType(key string) keyKind {
	if key == publication.ArchiveKey {
		return referenceKey
	}
	if len(key) > 1024 {
		return invalidKey
	}
	p := strings.Split(key, "/")
	if len(p) != 10 && len(p) != 11 {
		return invalidKey
	}
	if p[0] != publication.Prefix || p[1] != "archives" || p[3] != "machines" || p[5] != "agents" || p[7] != "sessions" {
		return invalidKey
	}
	for _, n := range []int{2, 4, 6, 8} {
		if !canonicalHex(p[n], 0) {
			return invalidKey
		}
	}
	if len(p) == 10 {
		if p[9] == "current.json" {
			return referenceKey
		}
		return invalidKey
	}
	if p[9] == "manifests" && strings.HasSuffix(p[10], ".json") && canonicalHex(strings.TrimSuffix(p[10], ".json"), 64) {
		return manifestKey
	}
	if p[9] == "objects" && strings.HasSuffix(p[10], ".zst") && canonicalHex(strings.TrimSuffix(p[10], ".zst"), 64) {
		return objectKey
	}
	return invalidKey
}

// safeError deliberately excludes SDK diagnostics, which can contain endpoint,
// credential, key, or response-body data. Callers classify with errors.Is.
func safeError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if response, ok := errors.AsType[*smithyhttp.ResponseError](err); ok {
		switch response.HTTPStatusCode() {
		case http.StatusNotFound:
			return publication.ErrNotFound
		case http.StatusPreconditionFailed, http.StatusConflict:
			return publication.ErrConflict
		case http.StatusUnauthorized, http.StatusForbidden:
			return ErrDestinationPaused
		}
	}
	return errors.New("publication destination operation failed")
}

// Open's deadline remains active for the entire response body lifetime. Callers
// must close the returned stream even when they stop before EOF.
func (b *Backend) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	kind := keyType(key)
	if kind == invalidKey {
		return nil, errors.New("invalid publication key")
	}
	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	out, err := b.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(b.bucket), Key: aws.String(key)})
	if err != nil {
		defer cancel()
		return nil, safeError(ctx, err)
	}
	limit := int64(publication.MaxMetadata)
	if kind == objectKey {
		limit = MaxObjectSize
	}
	if out.Body == nil || out.ContentLength == nil || *out.ContentLength < 0 || *out.ContentLength > limit {
		cancel()
		if out.Body != nil {
			_ = out.Body.Close() // Invalid response; no writable data to flush.
		}
		return nil, errors.New("invalid publication response length")
	}
	return &stream{ctx: ctx, cancel: cancel, body: out.Body, remaining: *out.ContentLength}, nil
}

type stream struct {
	ctx       context.Context
	cancel    context.CancelFunc
	body      io.ReadCloser
	remaining int64
	once      sync.Once
	closeErr  error
}

func (s *stream) Read(p []byte) (int, error) {
	if err := s.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := s.body.Read(p)
	s.remaining -= int64(n)
	if s.remaining < 0 {
		return n, errors.New("publication response length mismatch")
	}
	if errors.Is(err, io.EOF) && s.remaining != 0 {
		return n, io.ErrUnexpectedEOF
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return n, safeError(s.ctx, err)
	}
	return n, err
}

func (s *stream) Close() error {
	s.once.Do(func() { s.cancel(); s.closeErr = s.body.Close() })
	if s.closeErr != nil {
		return errors.New("publication stream close failed")
	}
	return nil
}

// PutFile verifies and conditionally creates one bounded regular file. Keys are supplied
// by the publication writer; current/archive references cannot be written here.
func (b *Backend) PutFile(ctx context.Context, key, path string) (err error) {
	kind := keyType(key)
	if kind != manifestKey && kind != objectKey {
		return errors.New("invalid publication payload key")
	}
	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	// Nonblocking open lets us reject FIFOs/devices without waiting for a peer.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return errors.New("cannot open publication payload")
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			err = errors.Join(err, errors.New("publication payload close failed"))
		}
	}()
	info, err := f.Stat()
	if err != nil {
		return errors.New("cannot stat publication payload")
	}
	limit := MaxObjectSize
	if kind == manifestKey {
		limit = publication.MaxMetadata
	}
	if !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > limit {
		return errors.New("invalid publication payload size or type")
	}
	// Verify before any network call; spool ownership must prevent concurrent
	// mutation throughout verification and SDK signing/upload passes.
	digest := strings.TrimSuffix(key[strings.LastIndex(key, "/")+1:], ".zst")
	digest = strings.TrimSuffix(digest, ".json")
	if err := verifyDigest(ctx, io.NewSectionReader(f, 0, info.Size()), info.Size(), digest); err != nil {
		return err
	}
	_, err = b.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(b.bucket), Key: aws.String(key),
		Body: io.NewSectionReader(f, 0, info.Size()), ContentLength: aws.Int64(info.Size()),
		IfNoneMatch: aws.String("*"),
	}, func(o *s3.Options) { o.RetryMaxAttempts = 1 })
	if err == nil {
		return nil
	}
	writeErr := safeError(ctx, err)
	if ctx.Err() != nil || errors.Is(writeErr, ErrDestinationPaused) {
		return writeErr
	}
	// A collision or lost acknowledgement is success only after verifying exact
	// existing bytes. Never delete corruption or retry with an unguarded PUT.
	return b.reconcileImmutable(ctx, key, info.Size(), digest, writeErr)
}

// verifyDigest bounds memory and reads one overflow byte to detect excess data.
func verifyDigest(ctx context.Context, r io.Reader, size int64, digest string) error {
	h := sha256.New()
	r = io.LimitReader(r, size+1)
	buf := make([]byte, 32*1024)
	var count int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := r.Read(buf)
		count += int64(n)
		_, _ = h.Write(buf[:n]) // hash.Hash.Write never returns an error.
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return safeError(ctx, err)
		}
	}
	if count != size || hex.EncodeToString(h.Sum(nil)) != digest {
		return publication.ErrConflict
	}
	return ctx.Err()
}

func (b *Backend) reconcileImmutable(ctx context.Context, key string, size int64, digest string, writeErr error) error {
	out, err := b.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(b.bucket), Key: aws.String(key)})
	if err != nil {
		readErr := safeError(ctx, err)
		if errors.Is(readErr, publication.ErrNotFound) {
			return writeErr // Absence does not prove an ambiguous PUT cannot commit.
		}
		return readErr
	}
	if out.Body == nil {
		return publication.ErrConflict
	}
	var verifyErr error
	if out.ContentLength == nil || *out.ContentLength != size {
		verifyErr = publication.ErrConflict
	} else {
		verifyErr = verifyDigest(ctx, out.Body, size, digest)
	}
	closeErr := out.Body.Close()
	if closeErr != nil {
		return errors.Join(verifyErr, errors.New("publication stream close failed"))
	}
	return verifyErr
}

// Current returns metadata and the exact opaque ETag of the same GET response.
func (b *Backend) Current(ctx context.Context, key string) ([]byte, string, error) {
	if keyType(key) != referenceKey {
		return nil, "", errors.New("invalid publication reference key")
	}
	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	out, err := b.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(b.bucket), Key: aws.String(key)})
	if err != nil {
		return nil, "", safeError(ctx, err)
	}
	if out.Body == nil {
		return nil, "", errors.New("missing publication metadata body")
	}
	data, readErr := io.ReadAll(io.LimitReader(out.Body, publication.MaxMetadata+1))
	closeErr := out.Body.Close()
	if readErr != nil {
		return nil, "", safeError(ctx, readErr)
	}
	if closeErr != nil {
		return nil, "", errors.New("publication metadata close failed")
	}
	if len(data) > publication.MaxMetadata || (out.ContentLength != nil && *out.ContentLength != int64(len(data))) {
		return nil, "", errors.New("invalid publication metadata length")
	}
	if !validToken(aws.ToString(out.ETag)) {
		return nil, "", errors.New("missing or invalid publication version token")
	}
	return data, *out.ETag, nil
}

func validToken(token string) bool {
	if len(token) < 3 || len(token) > 1024 || token[0] != '"' || token[len(token)-1] != '"' {
		return false
	}
	for _, c := range token[1 : len(token)-1] {
		if c < 0x21 || c > 0x7e || c == '"' {
			return false
		}
	}
	return true
}

// CompareAndSwap is one native conditional PutObject, never HEAD/copy/multipart.
// An empty token means create-only; otherwise the exact GET ETag is required.
func (b *Backend) CompareAndSwap(ctx context.Context, key, expectedToken string, body []byte) error {
	if keyType(key) != referenceKey || len(body) > publication.MaxMetadata || (expectedToken != "" && !validToken(expectedToken)) || (key == publication.ArchiveKey && expectedToken != "") {
		return errors.New("invalid publication conditional write")
	}
	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	in := &s3.PutObjectInput{Bucket: aws.String(b.bucket), Key: aws.String(key), Body: bytes.NewReader(body), ContentLength: aws.Int64(int64(len(body)))}
	if expectedToken == "" {
		in.IfNoneMatch = aws.String("*")
	} else {
		in.IfMatch = aws.String(expectedToken)
	}
	_, err := b.client.PutObject(ctx, in, func(o *s3.Options) { o.RetryMaxAttempts = 1 })
	return safeError(ctx, err)
}
