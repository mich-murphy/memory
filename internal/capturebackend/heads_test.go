package capturebackend

import (
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/mich-murphy/memory/internal/publication"
	"github.com/mich-murphy/memory/internal/publicationr2"
)

func TestFactoryListsBoundedSuccessfulXMLForDefaultAndInjectedClients(t *testing.T) {
	for _, defaultTransport := range []bool{false, true} {
		t.Run(map[bool]string{false: "injected", true: "default-transport"}[defaultTransport], func(t *testing.T) {
			body := &countedErrorBody{}
			calls := 0
			rt := roundTrip(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Query().Get("list-type") != "2" || r.URL.Query().Get("delimiter") != "/" || r.URL.Query().Get("max-keys") != "256" || !strings.Contains(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256") {
					t.Error("not a signed bounded LIST")
				}
				return &http.Response{StatusCode: http.StatusOK, Body: body, ContentLength: -1, Header: make(http.Header)}, nil
			})
			var client aws.HTTPClient = &http.Client{Transport: rt}
			if defaultTransport {
				c := defaultHTTPClient()
				c.Transport = rt
				client = c
			}
			d := deps(rt)
			d.HTTPClient = client
			b, err := Open(t.Context(), spec(), d)
			if err != nil {
				t.Fatal(err)
			}
			lister, ok := b.(publication.SessionNamespaceLister)
			if !ok {
				t.Fatal("factory dropped listing capability")
			}
			_, _, err = lister.ListSessionNamespaces(t.Context(), publication.Identity{ArchiveID: "archive", MachineID: "BC-machine", Agent: "pi"}, "", 256)
			if err == nil || strings.Contains(err.Error(), "xxxx") || calls != 1 || body.read != publicationr2.MaxListResponseBytes+1 || body.closes != 1 {
				t.Fatalf("bound failed calls=%d bytes=%d closes=%d err=%v", calls, body.read, body.closes, err)
			}
		})
	}
}
