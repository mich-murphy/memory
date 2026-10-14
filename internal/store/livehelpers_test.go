package store_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mich-murphy/memory/internal/config"
	"github.com/mich-murphy/memory/internal/store"
)

func TestR2TestMultipart(t *testing.T) {
	for _, scenario := range []string{"ownedPaginated", "foreign", "parentEscape", "abortFailure", "listFailure", "invalidMarker"} {
		t.Run(scenario, func(t *testing.T) {
			namespace, err := newNamespace(store.NewLocal(t.TempDir()), 2)
			acceptanceOK(t, "newNamespace", err)
			prefix := namespace.prefix
			lists := 0
			var aborted []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				query := r.URL.Query()
				switch r.Method {
				case http.MethodGet:
					lists++
					if query.Get("prefix") != prefix || query.Get("max-uploads") != "2" || !query.Has("uploads") || r.URL.Path != "/acceptance" {
						t.Error("multipart LIST was not confined to exact owned prefix")
					}
					if marker := query.Get("key-marker"); marker != "" && !strings.HasPrefix(marker, prefix) {
						t.Error("multipart marker was unconfined")
					}
					w.Header().Set("Content-Type", "application/xml")
					if scenario == "listFailure" {
						w.WriteHeader(http.StatusForbidden)
						_, err := fmt.Fprint(w, "<Error><Code>AccessDenied</Code><Message>FAKE_AUTHORIZATION_SENTINEL</Message></Error>")
						if err != nil {
							t.Error("fake response write failed")
						}
						return
					}
					var contents string
					truncated := false
					marker := prefix + "one"
					switch {
					case lists == 1:
						contents = fmt.Sprintf("<Upload><Key>%sone</Key><UploadId>owned-one</UploadId></Upload>", prefix)
						switch scenario {
						case "ownedPaginated":
							truncated = true
						case "foreign":
							contents += "<Upload><Key>v3/untouchable</Key><UploadId>foreign</UploadId></Upload>"
						case "parentEscape":
							contents += fmt.Sprintf("<Upload><Key>%s../untouchable</Key><UploadId>foreign</UploadId></Upload>", prefix)
						case "abortFailure":
							contents += fmt.Sprintf("<Upload><Key>%stwo</Key><UploadId>owned-two</UploadId></Upload>", prefix)
						case "invalidMarker":
							truncated, marker = true, "v3/untouchable"
						case "listFailure":
							t.Error("unreachable listFailure response")
						}
					case lists == 2 && scenario == "ownedPaginated":
						if query.Get("key-marker") != prefix+"one" || query.Get("upload-id-marker") != "owned-one" {
							t.Error("multipart continuation markers differ")
						}
						contents = fmt.Sprintf("<Upload><Key>%stwo</Key><UploadId>owned-two</UploadId></Upload>", prefix)
					case scenario == "abortFailure":
						contents = fmt.Sprintf("<Upload><Key>%sone</Key><UploadId>owned-one</UploadId></Upload>", prefix)
					}
					_, err := fmt.Fprintf(w, "<ListMultipartUploadsResult><IsTruncated>%t</IsTruncated><NextKeyMarker>%s</NextKeyMarker><NextUploadIdMarker>owned-one</NextUploadIdMarker>%s</ListMultipartUploadsResult>", truncated, marker, contents)
					if err != nil {
						t.Error("fake response write failed")
					}
				case http.MethodDelete:
					id := query.Get("uploadId")
					key := strings.TrimPrefix(r.URL.Path, "/acceptance/")
					if key != prefix+"one" && key != prefix+"two" || id != "owned-one" && id != "owned-two" {
						t.Error("aborted unobserved or foreign multipart upload")
					}
					aborted = append(aborted, id)
					if scenario == "abortFailure" && id == "owned-one" {
						w.WriteHeader(http.StatusForbidden)
						_, err := fmt.Fprint(w, "<Error><Code>AccessDenied</Code><Message>FAKE_AUTHORIZATION_SENTINEL</Message></Error>")
						if err != nil {
							t.Error("fake response write failed")
						}
					} else {
						w.WriteHeader(http.StatusNoContent)
					}
				default:
					t.Error("unexpected multipart method")
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			t.Cleanup(server.Close)
			r2, err := store.NewR2(&config.Config{R2Endpoint: server.URL, R2Bucket: "acceptance", R2AccessKeyID: "fake", R2SecretAccessKey: "fake"})
			acceptanceOK(t, "NewR2", err)
			count, err := store.R2TestMultipart(t.Context(), r2, prefix, true)
			if scenario == "ownedPaginated" {
				acceptanceOK(t, "owned multipart cleanup", err)
				if count != 2 || lists != 3 {
					t.Fatalf("cleanup count, lists = %d, %d, want 2, 3", count, lists)
				}
			} else if err == nil || strings.Contains(err.Error(), "FAKE_AUTHORIZATION_SENTINEL") {
				t.Fatal("multipart helper accepted unsafe response or exposed raw error")
			}
			var want []string
			if scenario == "ownedPaginated" || scenario == "abortFailure" {
				want = []string{"owned-one", "owned-two"}
			}
			if diff := cmp.Diff(want, aborted); diff != "" {
				t.Fatalf("observed aborts (-want +got): %s", diff)
			}
		})
	}
}

func TestR2TestMultipartRejectsInvalidPrefixesBeforeRequest(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(server.Close)
	r2, err := store.NewR2(&config.Config{R2Endpoint: server.URL, R2Bucket: "acceptance", R2AccessKeyID: "fake", R2SecretAccessKey: "fake"})
	acceptanceOK(t, "NewR2", err)
	for _, prefix := range []string{"", "v3/", "_agent-sessions/test/short/", "_agent-sessions/test/../../outside/", "_agent-sessions/test/ABCDEFGHIJKLMNOPQRSTUVWXYZ/more/", "_agent-sessions/test/ABCDEFGHIJKLMNOPQRSTUVWXYZ", "_agent-sessions/test/abcdefghijklmnopqrstuvwxyz/", "_agent-sessions/test/ABCDEFGHIJKLMNOPQRSTUVWXY%/"} {
		if _, err := store.R2TestMultipart(t.Context(), r2, prefix, true); err == nil {
			t.Fatal("multipart helper accepted invalid prefix")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := store.R2TestMultipart(ctx, r2, "_agent-sessions/test/ABCDEFGHIJKLMNOPQRSTUVWXYZ/", false); err == nil {
		t.Fatal("multipart helper ignored cancelled context")
	}
	if calls != 0 {
		t.Fatalf("requests = %d, want 0", calls)
	}
}
