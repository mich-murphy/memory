package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

var (
	ErrNotFound = errors.New("store: object not found")
	ErrExists   = errors.New("store: object already exists")
)

type Object struct {
	Key      string
	Size     int64
	Metadata map[string]string
}

type Page struct {
	Objects []Object
	Next    string
}

// List returns at most limit objects in key order. Next is opaque to callers.
func (l *Local) List(ctx context.Context, prefix, token string, limit int) (Page, error) {
	if limit <= 0 || limit > 1000 {
		return Page{}, errors.New("store list: limit must be between 1 and 1000")
	}
	var objects listCandidates
	err := filepath.WalkDir(l.root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		object, err := l.listObject(path, entry, prefix, token)
		if err != nil {
			return err
		}
		if object == nil {
			return nil
		}
		objects.keep(*object, limit+1)
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return Page{}, nil
	}
	if err != nil {
		return Page{}, fmt.Errorf("store list: %w", err)
	}
	slices.SortFunc(objects, func(a, b Object) int { return strings.Compare(a.Key, b.Key) })
	page := Page{Objects: objects}
	if len(objects) > limit {
		page.Objects = objects[:limit]
		page.Next = objects[limit-1].Key
	}
	return page, nil
}

func (r *R2) List(ctx context.Context, prefix, token string, limit int) (Page, error) {
	if limit <= 0 || limit > 1000 {
		return Page{}, errors.New("store list: limit must be between 1 and 1000")
	}
	input := &s3.ListObjectsV2Input{Bucket: &r.bucket, Prefix: &prefix, MaxKeys: aws.Int32(int32(limit))}
	if token != "" {
		input.ContinuationToken = &token
	}
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	out, err := r.client.ListObjectsV2(ctx, input)
	if err != nil {
		return Page{}, fmt.Errorf("store list: %w", err)
	}
	page := Page{Next: aws.ToString(out.NextContinuationToken)}
	for _, obj := range out.Contents {
		page.Objects = append(page.Objects, Object{Key: aws.ToString(obj.Key), Size: aws.ToInt64(obj.Size)})
	}
	if aws.ToBool(out.IsTruncated) && page.Next == "" {
		return Page{}, errors.New("store list: truncated page without continuation token")
	}
	return page, nil
}

func (l *Local) Head(ctx context.Context, key string) (Object, error) {
	if err := l.validateKey(key); err != nil {
		return Object{}, err
	}
	if err := ctx.Err(); err != nil {
		return Object{}, err
	}
	info, err := os.Stat(l.path(key))
	if errors.Is(err, fs.ErrNotExist) {
		return Object{}, ErrNotFound
	}
	if err != nil {
		return Object{}, fmt.Errorf("head %s: %w", key, err)
	}
	file, err := os.Open(l.path(key))
	if err != nil {
		return Object{}, err
	}
	defer func() { _ = file.Close() }() // read-only checksum
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return Object{}, err
	}
	return Object{Key: key, Size: info.Size(), Metadata: map[string]string{"content_sha256": hex.EncodeToString(digest.Sum(nil))}}, nil
}

func (r *R2) Head(ctx context.Context, key string) (Object, error) {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	out, err := r.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &r.bucket, Key: &key})
	if isNotFound(err) {
		return Object{}, ErrNotFound
	}
	if err != nil {
		return Object{}, fmt.Errorf("head %s: %w", key, err)
	}
	return Object{Key: key, Size: aws.ToInt64(out.ContentLength), Metadata: out.Metadata}, nil
}

func (l *Local) OpenReader(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := l.validateKey(key); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := os.Open(l.path(key))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	return f, err
}

// OpenReader's deadline covers reading the body too, so it ends when the caller closes it.
func (r *R2) OpenReader(ctx context.Context, key string) (io.ReadCloser, error) {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	out, err := r.client.GetObject(ctx, &s3.GetObjectInput{Bucket: &r.bucket, Key: &key})
	if err != nil {
		cancel()
		if isNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get %s: %w", key, err)
	}
	return &cancelOnClose{ReadCloser: out.Body, cancel: cancel}, nil
}

type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelOnClose) Close() error {
	defer c.cancel()
	return c.ReadCloser.Close()
}

// Create publishes a small object only if absent. Local publication uses an atomic hard
// link of a completely written private file, never a visible partially written O_EXCL file.
func (l *Local) Create(ctx context.Context, key string, body []byte) (err error) {
	if err := l.validateKey(key); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	path := l.path(key)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	staging := filepath.Join(l.root, ".store-staging")
	if err := os.MkdirAll(staging, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(staging, "create-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }() // unpublished staging cleanup only
	_, err = f.Write(body)
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	if err := os.Link(f.Name(), path); errors.Is(err, fs.ErrExist) {
		return ErrExists
	} else if err != nil {
		return err
	}
	return nil
}

func (r *R2) Create(ctx context.Context, key string, body []byte) error {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	_, err := r.client.PutObject(ctx, &s3.PutObjectInput{Bucket: &r.bucket, Key: &key, Body: strings.NewReader(string(body)), ContentLength: aws.Int64(int64(len(body))), IfNoneMatch: aws.String("*")})
	if err != nil {
		if apiErr, ok := errors.AsType[interface {
			error
			ErrorCode() string
		}](err); ok && apiErr.ErrorCode() == "PreconditionFailed" {
			return ErrExists
		}
		return fmt.Errorf("create %s: %w", key, err)
	}
	return nil
}

// listCandidates is a max heap: only the smallest limit+1 keys are retained.
// WalkDir's directory order is not object-key order (a/x can precede a-b).
type listCandidates []Object

func (h *listCandidates) keep(object Object, capacity int) {
	if len(*h) < capacity {
		*h = append(*h, object)
		for child := len(*h) - 1; child > 0; {
			parent := (child - 1) / 2
			if (*h)[parent].Key >= (*h)[child].Key {
				break
			}
			(*h)[parent], (*h)[child] = (*h)[child], (*h)[parent]
			child = parent
		}
		return
	}
	if object.Key >= (*h)[0].Key {
		return
	}
	(*h)[0] = object
	for parent := 0; ; {
		child := 2*parent + 1
		if child >= len(*h) {
			return
		}
		if child+1 < len(*h) && (*h)[child+1].Key > (*h)[child].Key {
			child++
		}
		if (*h)[parent].Key >= (*h)[child].Key {
			return
		}
		(*h)[parent], (*h)[child] = (*h)[child], (*h)[parent]
		parent = child
	}
}

func (l *Local) listObject(path string, entry fs.DirEntry, prefix, token string) (*Object, error) {
	if entry.IsDir() && path == l.root {
		return nil, nil
	}
	key, err := filepath.Rel(l.root, path)
	if err != nil {
		return nil, err
	}
	key = filepath.ToSlash(key)
	if entry.IsDir() {
		if key == ".store-staging" || !listSubtreeMatches(key, prefix, token) {
			return nil, fs.SkipDir
		}
		return nil, nil
	}
	if !strings.HasPrefix(key, prefix) || key <= token {
		return nil, nil
	}
	info, err := entry.Info()
	if err != nil {
		return nil, err
	}
	return &Object{Key: key, Size: info.Size()}, nil
}

// A directory contains only keys beginning with key+"/". Intersect that range
// with the literal prefix and the exclusive token. Never clean a prefix or use
// it as a filesystem path: even ../, repeated slashes and OS-invalid strings
// are literal filters. Descending from the root also retains WalkDir's rule of
// never following directory symlinks, including in a narrow prefix's ancestors.
func listSubtreeMatches(key, prefix, token string) bool {
	subtree := key + "/"
	if !strings.HasPrefix(subtree, prefix) && !strings.HasPrefix(prefix, subtree) {
		return false
	}
	// '/' immediately precedes '0' in byte order, so key+"0" is an
	// exclusive upper bound for every key in this directory.
	return token < key+"0"
}
