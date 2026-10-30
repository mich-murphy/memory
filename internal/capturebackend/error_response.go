package capturebackend

import (
	"bytes"
	"errors"
	"io"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// The S3 SDK buffers error bodies before deserializing them. Bound those bytes
// here, before SDK middleware can read an untrusted response into memory.
const maxErrorResponseBytes = 64 << 10

var errErrorResponseTooLarge = errors.New("capture destination error response exceeds limit")

// boundedErrorClient leaves successful bodies streaming and delegates request
// signing, deadlines, redirect policy and retry policy unchanged. It wraps both
// factory-default and explicitly injected clients.
type boundedErrorClient struct {
	client aws.HTTPClient
}

// The received status is independent of whether its error body can be consumed.
// Retain it through SDK error wrapping so auth failures still pause the destination.
// Never retain the original request, headers, status text or private response bytes.
// Returning an error (not a successful synthetic response) keeps malformed bodies
// explicitly failed, while the existing safeError status mapping remains authoritative.
func boundedResponseError(status int, err error) (*http.Response, error) {
	response := &http.Response{StatusCode: status, Body: http.NoBody, Header: make(http.Header)}
	// Smithy also constructs an outer ResponseError from the returned response.
	// Supply the sanitized response alongside the error so that outer status is not
	// zero (which would shadow the inner status in errors.As). This aws.HTTPClient
	// wrapper sits outside net/http.Client, whose Do convention differs here.
	return response, &smithyhttp.ResponseError{Response: &smithyhttp.Response{Response: response}, Err: err}
}

func (c boundedErrorClient) Do(req *http.Request) (*http.Response, error) {
	response, err := c.client.Do(req)
	if err != nil || response == nil {
		return response, err
	}
	if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
		return response, nil
	}
	if response.Body == nil {
		return response, nil
	}
	// One overflow byte distinguishes an exactly-full body from a truncated one.
	// Close the original on every outcome; never drain an unbounded error body.
	body, readErr := io.ReadAll(io.LimitReader(response.Body, maxErrorResponseBytes+1))
	closeErr := response.Body.Close()
	if len(body) > maxErrorResponseBytes {
		return boundedResponseError(response.StatusCode, errErrorResponseTooLarge)
	}
	if readErr != nil {
		return boundedResponseError(response.StatusCode, errors.New("capture destination error response read failed"))
	}
	if closeErr != nil {
		return boundedResponseError(response.StatusCode, errors.New("capture destination error response close failed"))
	}
	// Preserve status and headers so normal bounded auth/conflict/not-found
	// responses retain the SDK's existing classification. Only error bodies are
	// replayed from bounded memory; successful object streams are never buffered.
	response.Body = io.NopCloser(bytes.NewReader(body))
	return response, nil
}
