package store

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // Content-MD5 is S3's per-part integrity header, not a security use
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	"github.com/mich-murphy/memory/internal/config"
)

// Variables so tests can shrink them.
var (
	singlePutMax int64 = 64 << 20
	// R2 needs every part but the last to be the same size.
	partSize = 16 << 20
)

const (
	opTimeout  = 10 * time.Minute
	maxRetries = 5
)

type R2 struct {
	client *s3.Client
	bucket string
}

func NewR2(cfg *config.Config) (*R2, error) {
	if missing := cfg.MissingR2Settings(); len(missing) > 0 {
		return nil, fmt.Errorf("missing R2 settings: %s", strings.Join(missing, ", "))
	}
	httpClient := awshttp.NewBuildableClient().
		WithDialerOptions(func(d *net.Dialer) {
			d.Timeout = 15 * time.Second
			d.KeepAlive = 30 * time.Second
		}).
		WithTransportOptions(func(t *http.Transport) {
			t.TLSHandshakeTimeout = 15 * time.Second
			t.ResponseHeaderTimeout = 2 * time.Minute
		})
	client := s3.New(s3.Options{
		Region:       "auto",
		BaseEndpoint: aws.String(cfg.R2Endpoint),
		// R2 takes either addressing style; path style also works against local S3 fakes.
		UsePathStyle: true,
		Credentials:  credentials.NewStaticCredentialsProvider(cfg.R2AccessKeyID, cfg.R2SecretAccessKey, ""),
		HTTPClient:   httpClient,
		Retryer: retry.NewStandard(func(o *retry.StandardOptions) {
			o.MaxAttempts = maxRetries
		}),
		// We send a SHA-256 of every body ourselves (R2 rejects a mismatch), so the SDK's own
		// CRC32 adds CPU for nothing.
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	})
	return &R2{client: client, bucket: cfg.R2Bucket}, nil
}

func sha256Base64(body []byte) string {
	sum := sha256.Sum256(body)
	return base64.StdEncoding.EncodeToString(sum[:])
}

func (r *R2) Put(ctx context.Context, key string, body []byte, meta map[string]string) error {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	_, err := r.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:         &r.bucket,
		Key:            &key,
		Body:           bytes.NewReader(body),
		ContentLength:  aws.Int64(int64(len(body))),
		Metadata:       meta,
		ChecksumSHA256: aws.String(sha256Base64(body)),
	})
	if err != nil {
		return fmt.Errorf("put %s: %w", key, err)
	}
	return nil
}

func (r *R2) PutFile(ctx context.Context, key, path string, meta map[string]string) error {
	st, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("put %s: %w", key, err)
	}
	if st.Size() <= singlePutMax {
		body, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("put %s: %w", key, err)
		}
		return r.Put(ctx, key, body, meta)
	}
	return r.putMultipart(ctx, key, path, meta)
}

func (r *R2) putMultipart(ctx context.Context, key, path string, meta map[string]string) (err error) {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("put %s: %w", key, err)
	}
	defer func() { _ = f.Close() }() // read-only
	up, err := r.client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{Bucket: &r.bucket, Key: &key, Metadata: meta})
	if err != nil {
		return fmt.Errorf("put %s: %w", key, err)
	}
	defer func() {
		if err != nil {
			// Best effort; R2 also expires abandoned uploads on its own.
			abortCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
			defer cancel()
			_, _ = r.client.AbortMultipartUpload(abortCtx, &s3.AbortMultipartUploadInput{Bucket: &r.bucket, Key: &key, UploadId: up.UploadId})
		}
	}()
	var parts []types.CompletedPart
	buf := make([]byte, partSize)
	for n := int32(1); ; n++ {
		size, rerr := io.ReadFull(f, buf)
		if size > 0 {
			part, err := r.uploadPart(ctx, key, up.UploadId, n, buf[:size])
			if err != nil {
				return err
			}
			parts = append(parts, part)
		}
		if errors.Is(rerr, io.EOF) || errors.Is(rerr, io.ErrUnexpectedEOF) {
			break
		}
		if rerr != nil {
			return fmt.Errorf("put %s: %w", key, rerr)
		}
	}
	completeCtx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	_, err = r.client.CompleteMultipartUpload(completeCtx, &s3.CompleteMultipartUploadInput{
		Bucket: &r.bucket, Key: &key, UploadId: up.UploadId,
		MultipartUpload: &types.CompletedMultipartUpload{Parts: parts},
	})
	if err != nil {
		return fmt.Errorf("put %s: %w", key, err)
	}
	return nil
}

func (r *R2) uploadPart(ctx context.Context, key string, uploadID *string, n int32, body []byte) (types.CompletedPart, error) {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	sum := md5.Sum(body) //nolint:gosec // integrity header R2 accepts on UploadPart
	out, err := r.client.UploadPart(ctx, &s3.UploadPartInput{
		Bucket: &r.bucket, Key: &key, UploadId: uploadID, PartNumber: aws.Int32(n),
		Body: bytes.NewReader(body), ContentLength: aws.Int64(int64(len(body))),
		ContentMD5: aws.String(base64.StdEncoding.EncodeToString(sum[:])),
	})
	if err != nil {
		return types.CompletedPart{}, fmt.Errorf("put %s part %d: %w", key, n, err)
	}
	return types.CompletedPart{ETag: out.ETag, PartNumber: aws.Int32(n)}, nil
}

func (r *R2) Copy(ctx context.Context, src, dst string) (srcExists bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	_, err = r.client.CopyObject(ctx, &s3.CopyObjectInput{Bucket: &r.bucket, Key: &dst, CopySource: aws.String(r.bucket + "/" + src)})
	if isNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("copy %s: %w", src, err)
	}
	return true, nil
}

func (r *R2) Exists(ctx context.Context, key string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	_, err := r.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &r.bucket, Key: &key})
	if isNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("head %s: %w", key, err)
	}
	return true, nil
}

func (r *R2) Delete(ctx context.Context, key string) error {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	if _, err := r.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &r.bucket, Key: &key}); err != nil {
		return fmt.Errorf("delete %s: %w", key, err)
	}
	return nil
}

func (r *R2) Get(ctx context.Context, key string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	out, err := r.client.GetObject(ctx, &s3.GetObjectInput{Bucket: &r.bucket, Key: &key})
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", key, err)
	}
	defer func() { _ = out.Body.Close() }() // read-only
	data, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", key, err)
	}
	return data, nil
}

func (r *R2) List(ctx context.Context, prefix string) ([]string, error) {
	var keys []string
	pages := s3.NewListObjectsV2Paginator(r.client, &s3.ListObjectsV2Input{Bucket: &r.bucket, Prefix: &prefix})
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list %s: %w", prefix, err)
		}
		for _, o := range page.Contents {
			keys = append(keys, aws.ToString(o.Key))
		}
	}
	return keys, nil
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	if apiErr, ok := errors.AsType[smithy.APIError](err); ok {
		switch apiErr.ErrorCode() {
		case "NoSuchKey", "NotFound", "404":
			return true
		}
	}
	return false
}
