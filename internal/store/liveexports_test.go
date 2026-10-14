package store

import (
	"context"
	"errors"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/mich-murphy/memory/internal/raw"
)

// R2TestMultipart inspects only an exact random test prefix. With clean it aborts
// only upload IDs observed there and then verifies no owned uploads remain. This
// test-only export keeps private SDK access out of production and acceptance code.
func R2TestMultipart(ctx context.Context, r *R2, prefix string, clean bool) (int, error) {
	nonce := strings.TrimSuffix(strings.TrimPrefix(prefix, "_agent-sessions/test/"), "/")
	if !strings.HasPrefix(prefix, "_agent-sessions/test/") || !strings.HasSuffix(prefix, "/") || len(nonce) < 26 || strings.Contains(nonce, "/") || !raw.ValidKey(nonce) || strings.Trim(nonce, "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567") != "" {
		return 0, errors.New("test multipart: invalid owned prefix")
	}
	uploads, err := r2TestUploads(ctx, r, prefix)
	if err != nil || !clean {
		return len(uploads), err
	}
	var failures error
	for _, upload := range uploads {
		_, err := r.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{Bucket: &r.bucket, Key: upload.Key, UploadId: upload.UploadId})
		if err != nil {
			failures = errors.Join(failures, errors.New("test multipart: abort failed (details withheld)"))
		}
	}
	remaining, err := r2TestUploads(ctx, r, prefix)
	if err != nil {
		failures = errors.Join(failures, err)
	} else if len(remaining) != 0 {
		failures = errors.Join(failures, errors.New("test multipart: owned uploads remain"))
	}
	return len(uploads), failures
}

func r2TestUploads(ctx context.Context, r *R2, prefix string) ([]types.MultipartUpload, error) {
	var uploads []types.MultipartUpload
	var keyMarker, idMarker *string
	seen := map[string]bool{}
	for range 10000 {
		out, err := r.client.ListMultipartUploads(ctx, &s3.ListMultipartUploadsInput{Bucket: &r.bucket, Prefix: &prefix, KeyMarker: keyMarker, UploadIdMarker: idMarker, MaxUploads: aws.Int32(2)})
		if err != nil {
			return nil, errors.New("test multipart: prefix listing failed (details withheld)")
		}
		for _, upload := range out.Uploads {
			key, id := aws.ToString(upload.Key), aws.ToString(upload.UploadId)
			if !strings.HasPrefix(key, prefix) || !raw.ValidKey(key) || id == "" || seen[key+"\x00"+id] {
				return nil, errors.New("test multipart: unconfined or repeated upload response")
			}
			seen[key+"\x00"+id] = true
			uploads = append(uploads, upload)
		}
		if !aws.ToBool(out.IsTruncated) {
			return uploads, nil
		}
		if !raw.ValidKey(aws.ToString(out.NextKeyMarker)) || !strings.HasPrefix(aws.ToString(out.NextKeyMarker), prefix) || aws.ToString(out.NextKeyMarker) == aws.ToString(keyMarker) && aws.ToString(out.NextUploadIdMarker) == aws.ToString(idMarker) {
			return nil, errors.New("test multipart: invalid pagination response")
		}
		keyMarker, idMarker = out.NextKeyMarker, out.NextUploadIdMarker
	}
	return nil, errors.New("test multipart: page bound exceeded")
}
