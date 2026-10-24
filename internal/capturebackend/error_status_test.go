package capturebackend

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/mich-murphy/memory/internal/publication"
	"github.com/mich-murphy/memory/internal/publicationr2"
)

func statusFailureBody(mode string) *countedErrorBody {
	switch mode {
	case "read":
		return &countedErrorBody{readErr: errors.New("PRIVATE_READ")}
	case "close":
		return &countedErrorBody{readErr: io.EOF, closeErr: errors.New("PRIVATE_CLOSE")}
	default:
		return &countedErrorBody{}
	}
}

func TestFailedErrorBodiesPreserveStatusThroughSDK(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusPreconditionFailed, http.StatusInternalServerError} {
		for _, mode := range []string{"overflow", "read", "close"} {
			t.Run(http.StatusText(status)+"/"+mode, func(t *testing.T) {
				body := statusFailureBody(mode)
				calls := 0
				rt := roundTrip(func(*http.Request) (*http.Response, error) {
					calls++
					return &http.Response{StatusCode: status, Status: "PRIVATE_STATUS", Body: body, Header: http.Header{"Private": []string{"PRIVATE_HEADER"}}, ContentLength: -1}, nil
				})
				sdk := s3.New(s3.Options{Region: "auto", BaseEndpoint: aws.String(spec().Endpoint), UsePathStyle: true, Credentials: aws.AnonymousCredentials{}, HTTPClient: boundedErrorClient{client: &http.Client{Transport: rt}}, Retryer: aws.NopRetryer{}})
				_, err := sdk.GetObject(t.Context(), &s3.GetObjectInput{Bucket: aws.String(spec().Bucket), Key: aws.String(identity().CurrentKey())})
				var responseErr *smithyhttp.ResponseError
				if err == nil || !errors.As(err, &responseErr) || responseErr.HTTPStatusCode() != status {
					t.Fatal("SDK lost typed HTTP status failure")
				}
				if strings.Contains(err.Error(), "PRIVATE") || responseErr.Response.Body != http.NoBody || len(responseErr.Response.Header) != 0 || responseErr.Response.Request != nil || responseErr.Response.Status != "" {
					t.Fatal("SDK error retained private response fields")
				}
				if mode == "overflow" && !errors.Is(err, errErrorResponseTooLarge) {
					t.Fatal("SDK lost explicit overflow sentinel")
				}
				if body.closes != 1 || calls != 1 || body.read > maxErrorResponseBytes+1 {
					t.Fatal("error body closure/read/request bound changed")
				}
			})
		}
	}
}

func TestFailedErrorBodiesPreserveBackendClassification(t *testing.T) {
	for status, want := range map[int]error{
		http.StatusUnauthorized:       publicationr2.ErrDestinationPaused,
		http.StatusForbidden:          publicationr2.ErrDestinationPaused,
		http.StatusNotFound:           publication.ErrNotFound,
		http.StatusConflict:           publication.ErrConflict,
		http.StatusPreconditionFailed: publication.ErrConflict,
	} {
		for _, mode := range []string{"overflow", "read", "close"} {
			for _, operation := range []string{"current", "cas", "put"} {
				t.Run(http.StatusText(status)+"/"+mode+"/"+operation, func(t *testing.T) {
					calls := 0
					var bodies []*countedErrorBody
					b := factory(t, roundTrip(func(*http.Request) (*http.Response, error) {
						calls++
						body := statusFailureBody(mode)
						bodies = append(bodies, body)
						return &http.Response{StatusCode: status, Body: body, ContentLength: -1, Header: make(http.Header)}, nil
					}))
					var err error
					switch operation {
					case "current":
						_, _, err = b.Current(t.Context(), identity().CurrentKey())
					case "cas":
						err = b.CompareAndSwap(t.Context(), identity().CurrentKey(), "", []byte("synthetic"))
					case "put":
						path, digest := writeErrorTestPayload(t)
						err = b.PutFile(t.Context(), identity().BaseKey()+"/manifests/"+digest+".json", path)
					}
					if !errors.Is(err, want) || strings.Contains(err.Error(), "PRIVATE") {
						t.Fatal("body failure changed backend status classification or leaked content")
					}
					wantCalls := 1
					// Non-auth immutable failures retain exact-byte GET reconciliation.
					// A failed/malformed GET is never successful immutable publication.
					if operation == "put" && !errors.Is(want, publicationr2.ErrDestinationPaused) {
						wantCalls = 2
					}
					if calls != wantCalls {
						t.Fatal("SDK retry or reconciliation/auth-pause regression")
					}
					for _, body := range bodies {
						if body.closes != 1 || body.read > maxErrorResponseBytes+1 {
							t.Fatal("original error body not bounded/closed")
						}
					}
				})
			}
		}
	}
}
