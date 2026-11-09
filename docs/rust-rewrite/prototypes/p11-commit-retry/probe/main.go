// p9 probe: conditional PUT/GET latency and If-Match race behaviour under ONE prefix.
// Credentials come from the environment only; nothing about them or the endpoint is printed.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

func client() (*s3.Client, string) {
	c := s3.New(s3.Options{
		Region:           "auto",
		BaseEndpoint:     aws.String(os.Getenv("R2_ENDPOINT")),
		Credentials:      credentials.NewStaticCredentialsProvider(os.Getenv("R2_ACCESS_KEY_ID"), os.Getenv("R2_SECRET_ACCESS_KEY"), ""),
		UsePathStyle:     true,
		RetryMaxAttempts: 1, // DESIGN 7.4 step 3: no client-level retries
	})
	return c, os.Getenv("R2_BUCKET")
}

// status returns an HTTP status (0 = transport error) without echoing the error text.
func status(err error) int {
	if err == nil {
		return 200
	}
	var re *awshttp.ResponseError
	if errors.As(err, &re) {
		return re.HTTPStatusCode()
	}
	return 0
}

type sample struct {
	Op     string  `json:"op"`
	Status int     `json:"status"`
	Ms     float64 `json:"ms"`
}

var (
	mu      sync.Mutex
	samples []sample
	reqs    int
)

func rec(op string, st int, d time.Duration) {
	mu.Lock()
	samples = append(samples, sample{op, st, float64(d.Microseconds()) / 1000})
	reqs++
	mu.Unlock()
}

func put(ctx context.Context, c *s3.Client, b, key, body, ifMatch string, none bool, op string) (string, int) {
	in := &s3.PutObjectInput{Bucket: &b, Key: &key, Body: bytes.NewReader([]byte(body))}
	if ifMatch != "" {
		in.IfMatch = aws.String(ifMatch)
	}
	if none {
		in.IfNoneMatch = aws.String("*")
	}
	t := time.Now()
	out, err := c.PutObject(ctx, in)
	d := time.Since(t)
	st := status(err)
	rec(op, st, d)
	if err != nil {
		return "", st
	}
	return aws.ToString(out.ETag), st
}

func get(ctx context.Context, c *s3.Client, b, key string) (string, string, int) {
	t := time.Now()
	out, err := c.GetObject(ctx, &s3.GetObjectInput{Bucket: &b, Key: &key})
	st := status(err)
	var body []byte
	etag := ""
	if err == nil {
		body, _ = io.ReadAll(out.Body)
		out.Body.Close()
		etag = aws.ToString(out.ETag)
	}
	rec("get", st, time.Since(t))
	return etag, string(body), st
}

func doc(n int, who string) string {
	return fmt.Sprintf(`{"schema":1,"seq":%d,"writer":"%s","manifest":"%064d","pad":"%0*d"}`, n, who, n, 120, 0)
}

func main() {
	ctx := context.Background()
	c, b := client()
	prefix := os.Args[2] // rust-acceptance/p9-<ts>/
	switch os.Args[1] {
	case "run":
		out := map[string]any{}
		// A: If-None-Match create, 200 unique keys
		for i := 0; i < 200; i++ {
			put(ctx, c, b, fmt.Sprintf("%screate/%03d.json", prefix, i), doc(i, "a"), "", true, "put_create")
		}
		// create again -> 412 expected
		dup := 0
		for i := 0; i < 20; i++ {
			if _, st := put(ctx, c, b, fmt.Sprintf("%screate/%03d.json", prefix, i), doc(i, "a"), "", true, "put_create_exists"); st == 412 {
				dup++
			}
		}
		out["create_exists_412"] = dup
		// B: If-Match chain on one key, 200 updates, each GET-free (etag from PUT reply)
		key := prefix + "current.json"
		etag, _ := put(ctx, c, b, key, doc(0, "a"), "", true, "put_create_current")
		var stale []string
		for i := 1; i <= 200; i++ {
			e, st := put(ctx, c, b, key, doc(i, "a"), etag, false, "put_ifmatch")
			if st != 200 {
				out["chain_break"] = i
				break
			}
			if i%7 == 0 {
				stale = append(stale, etag)
			}
			etag = e
		}
		// C: stale ETag -> 412 (30 samples)
		bad := 0
		for _, s := range stale {
			if _, st := put(ctx, c, b, key, doc(999, "stale"), s, false, "put_stale"); st != 412 {
				bad++
			}
		}
		out["stale_not_412"] = bad
		// D: GET latency 200
		for i := 0; i < 200; i++ {
			get(ctx, c, b, key)
		}
		// E: race, 60 rounds, two concurrent writers with same If-Match
		type race struct {
			A, B             int
			Winner           string
			Final            string
			LoserSeesOwnBody bool
		}
		var races []race
		for r := 0; r < 60; r++ {
			cur, _, _ := get(ctx, c, b, key)
			var wg sync.WaitGroup
			var sa, sb int
			wg.Add(2)
			go func() { defer wg.Done(); _, sa = put(ctx, c, b, key, doc(1000+r, "A"), cur, false, "race_put") }()
			go func() { defer wg.Done(); _, sb = put(ctx, c, b, key, doc(1000+r, "B"), cur, false, "race_put") }()
			wg.Wait()
			_, body, _ := get(ctx, c, b, key)
			fw := "?"
			if bytes.Contains([]byte(body), []byte(`"writer":"A"`)) {
				fw = "A"
			} else if bytes.Contains([]byte(body), []byte(`"writer":"B"`)) {
				fw = "B"
			}
			w := "none"
			if sa == 200 && sb != 200 {
				w = "A"
			} else if sb == 200 && sa != 200 {
				w = "B"
			} else if sa == 200 && sb == 200 {
				w = "both"
			}
			races = append(races, race{sa, sb, w, fw, false})
		}
		out["races"] = races
		out["samples"] = samples
		out["requests"] = reqs
		j, _ := json.Marshal(out)
		f, err := os.OpenFile(os.Args[3], os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			fmt.Fprintln(os.Stderr, "cannot create results file")
			os.Exit(1)
		}
		f.Write(j)
		f.Close()
		fmt.Println("requests", reqs)
	case "cleanup":
		if len(prefix) < 20 {
			fmt.Fprintln(os.Stderr, "prefix too short")
			os.Exit(1)
		}
		var ids []types.ObjectIdentifier
		p := s3.NewListObjectsV2Paginator(c, &s3.ListObjectsV2Input{Bucket: &b, Prefix: &prefix})
		for p.HasMorePages() {
			pg, err := p.NextPage(ctx)
			if err != nil {
				fmt.Fprintln(os.Stderr, "list failed", status(err))
				os.Exit(1)
			}
			for _, o := range pg.Contents {
				ids = append(ids, types.ObjectIdentifier{Key: o.Key})
			}
		}
		for i := 0; i < len(ids); i += 1000 {
			e := min(i+1000, len(ids))
			if _, err := c.DeleteObjects(ctx, &s3.DeleteObjectsInput{Bucket: &b, Delete: &types.Delete{Objects: ids[i:e], Quiet: aws.Bool(true)}}); err != nil {
				fmt.Fprintln(os.Stderr, "delete failed", status(err))
				os.Exit(1)
			}
		}
		left := 0
		p = s3.NewListObjectsV2Paginator(c, &s3.ListObjectsV2Input{Bucket: &b, Prefix: &prefix})
		for p.HasMorePages() {
			pg, err := p.NextPage(ctx)
			if err != nil {
				fmt.Fprintln(os.Stderr, "verify failed", status(err))
				os.Exit(1)
			}
			left += len(pg.Contents)
		}
		fmt.Println("deleted", len(ids), "remaining_under_prefix", left)
	}
	_ = http.StatusOK
}
