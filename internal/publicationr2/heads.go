package publicationr2

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/mich-murphy/memory/internal/publication"
)

// MaxListResponseBytes caps SDK XML buffering before deserialization.
const MaxListResponseBytes = 2 << 20

type boundedListClient struct{ client aws.HTTPClient }

func (c boundedListClient) Do(req *http.Request) (*http.Response, error) {
	response, err := c.client.Do(req)
	if err != nil || response == nil || response.StatusCode < 200 || response.StatusCode >= 300 || response.Body == nil {
		return response, err
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, MaxListResponseBytes+1))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil || len(body) > MaxListResponseBytes {
		return nil, errors.New("publication namespace response exceeds limit or cannot be read")
	}
	response.Body = io.NopCloser(bytes.NewReader(body))
	return response, nil
}

// ListSessionNamespaces performs one scoped delimiter LIST, never a bucket-wide
// scan. StartAfter is the last validated common-prefix, not an opaque S3 token;
// thus each cursor proves lexical advancement without accumulating token history.
func (b *Backend) ListSessionNamespaces(ctx context.Context, scope publication.Identity, after string, limit int) ([]string, bool, error) {
	prefix, err := publication.SessionPrefix(scope)
	if err != nil || limit < 1 || limit > 256 {
		return nil, false, errors.New("invalid namespace listing options")
	}
	if after != "" {
		if _, err = publication.SessionIdentity(scope, after); err != nil {
			return nil, false, err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	in := &s3.ListObjectsV2Input{Bucket: aws.String(b.bucket), Prefix: aws.String(prefix), Delimiter: aws.String("/"), MaxKeys: aws.Int32(int32(limit))}
	if after != "" {
		in.StartAfter = aws.String(after)
	}
	out, err := b.client.ListObjectsV2(ctx, in, func(o *s3.Options) { o.HTTPClient = boundedListClient{client: o.HTTPClient}; o.RetryMaxAttempts = 1 })
	if err != nil {
		return nil, false, safeError(ctx, err)
	}
	if out.IsTruncated == nil || len(out.Contents) != 0 || len(out.CommonPrefixes) > limit || (aws.ToBool(out.IsTruncated) && len(out.CommonPrefixes) == 0) {
		return nil, false, errors.New("invalid namespace listing response")
	}
	prefixes := make([]string, 0, len(out.CommonPrefixes))
	previous := after
	for _, entry := range out.CommonPrefixes {
		p := aws.ToString(entry.Prefix)
		if _, err = publication.SessionIdentity(scope, p); err != nil {
			return nil, false, err
		}
		if p <= previous {
			return nil, false, errors.New("nonadvancing namespace listing")
		}
		prefixes = append(prefixes, p)
		previous = p
	}
	return prefixes, !aws.ToBool(out.IsTruncated), nil
}
