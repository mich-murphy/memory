// Package capturebackend opens explicitly configured publication destinations.
// Validation never discovers credentials, accesses files, or makes requests.
package capturebackend

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/mich-murphy/memory/internal/publication"
	"github.com/mich-murphy/memory/internal/publicationr2"
)

type Spec struct {
	Kind         string `json:"kind"`
	LocalDir     string `json:"local_dir,omitempty"`
	Endpoint     string `json:"endpoint,omitempty"`
	Bucket       string `json:"bucket,omitempty"`
	AccessKeyEnv string `json:"access_key_env,omitempty"`
	SecretKeyEnv string `json:"secret_key_env,omitempty"`
	// MinThroughputKiB is the slowest sustained object transfer, in KiB/s, that
	// the size-aware body deadline assumes (30s + size/rate, clamped to 60s-15min).
	// Zero selects the default of 1024 KiB/s, a conservative unmeasured placeholder.
	MinThroughputKiB int `json:"min_throughput_kib,omitempty"`
}

// maxMinThroughputKiB bounds the setting at 1 GiB/s.
const maxMinThroughputKiB = 1 << 20

var (
	envName    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	r2Host     = regexp.MustCompile(`^[a-f0-9]{32}\.r2\.cloudflarestorage\.com$`)
	bucketName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)
)

func (s Spec) Validate() error {
	switch s.Kind {
	case "local":
		if !filepath.IsAbs(s.LocalDir) || filepath.Clean(s.LocalDir) != s.LocalDir || s.LocalDir == string(filepath.Separator) || !utf8.ValidString(s.LocalDir) || strings.ContainsAny(s.LocalDir, "\x00\r\n") {
			return errors.New("invalid local backend directory")
		}
		if s.Endpoint != "" || s.Bucket != "" || s.AccessKeyEnv != "" || s.SecretKeyEnv != "" || s.MinThroughputKiB != 0 {
			return errors.New("unused local backend options")
		}
	case "r2":
		if s.LocalDir != "" {
			return errors.New("unused R2 backend options")
		}
		u, err := url.Parse(s.Endpoint)
		if err != nil || u.Scheme != "https" || !r2Host.MatchString(u.Host) || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(s.Endpoint, "?#") {
			return errors.New("invalid R2 endpoint")
		}
		if s.MinThroughputKiB < 0 || s.MinThroughputKiB > maxMinThroughputKiB {
			return errors.New("invalid R2 upload throughput")
		}
		if !bucketName.MatchString(s.Bucket) || !envName.MatchString(s.AccessKeyEnv) || !envName.MatchString(s.SecretKeyEnv) || s.AccessKeyEnv == s.SecretKeyEnv {
			return errors.New("invalid R2 bucket or credential references")
		}
	default:
		return errors.New("unknown capture backend kind")
	}
	return nil
}

// ValidateIdentity checks the longest generated key, including hex expansion of
// UTF-8 bytes. It never truncates identities or changes the local key contract.
func ValidateIdentity(s Spec, id publication.Identity) error {
	if err := s.Validate(); err != nil {
		return err
	}
	if s.Kind != "r2" {
		return nil
	}
	for _, v := range []string{id.ArchiveID, id.MachineID, id.Agent, id.SessionID} {
		if v == "" || !utf8.ValidString(v) || strings.ContainsAny(v, "\x00\r\n") {
			return errors.New("invalid publication identity")
		}
	}
	if len(id.BaseKey()+"/manifests/"+strings.Repeat("0", 64)+".json") > 1024 {
		return errors.New("publication identity exceeds R2 key limit")
	}
	return nil
}

type Dependencies struct {
	LookupEnv  func(string) (string, bool)
	HTTPClient aws.HTTPClient
}

// Open performs no request. Only the two named environment references are read;
// there is no SDK default configuration, shared file, metadata or AWS chain.
func Open(ctx context.Context, s Spec, d Dependencies) (publication.Backend, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.Kind == "local" {
		return publication.NewLocal(s.LocalDir), nil
	}
	lookup := d.LookupEnv
	if lookup == nil {
		lookup = os.LookupEnv
	}
	access, ok := lookup(s.AccessKeyEnv)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !ok || access == "" {
		return nil, errors.New("capture destination credentials unavailable")
	}
	secret, ok := lookup(s.SecretKeyEnv)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !ok || secret == "" {
		return nil, errors.New("capture destination credentials unavailable")
	}
	client := d.HTTPClient
	if client == nil {
		client = defaultHTTPClient()
	}
	sdk := s3.New(s3.Options{
		Region: "auto", BaseEndpoint: aws.String(s.Endpoint), UsePathStyle: true,
		Credentials: credentials.NewStaticCredentialsProvider(access, secret, ""),
		HTTPClient:  boundedErrorClient{client: client}, Retryer: aws.NopRetryer{}, RetryMaxAttempts: 1,
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	})
	return publicationr2.New(sdk, s.Bucket, 30*time.Second, publicationr2.WithBodyBudget(s.bodyBudget()))
}

func (s Spec) bodyBudget() publicationr2.BodyBudget {
	budget := publicationr2.DefaultBodyBudget()
	if s.MinThroughputKiB > 0 {
		budget.MinThroughput = int64(s.MinThroughputKiB) << 10
	}
	return budget
}

// TransferBudget reports the worst-case PutFile duration of one object for this
// specification without opening it, so lazily bound destinations can size steps.
// Local backends add no allowance.
func (s Spec) TransferBudget(size int64) time.Duration {
	if s.Kind != "r2" {
		return 0
	}
	return 2 * s.bodyBudget().Total(size)
}

func defaultHTTPClient() *http.Client {
	// No whole-request Timeout: it would cap object bodies. Every operation carries a
	// context deadline (flat for metadata, size-aware for object bodies) and the
	// transport bounds dial, TLS and response-header waits.
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: &http.Transport{
		DialContext:            (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxResponseHeaderBytes: 1 << 20,
		Proxy:                  nil, MaxIdleConns: 16, MaxIdleConnsPerHost: 8, MaxConnsPerHost: 16,
		IdleConnTimeout: 60 * time.Second, TLSHandshakeTimeout: 10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second, ExpectContinueTimeout: time.Second,
	}}
}
