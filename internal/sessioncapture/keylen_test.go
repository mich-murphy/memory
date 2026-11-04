package sessioncapture

import (
	"errors"
	"strings"
	"testing"
)

func TestHintRefusesIdentityWhoseKeysExceedR2Limit(t *testing.T) {
	dir, o, r := setup(t)
	q := open(t, dir, o)
	// archive+machine+agent are 16 bytes; hex doubles each byte, so a 432-byte session
	// gives a 944-byte base key and exactly 1024 with the longest suffix.
	r.Identity.SessionID = strings.Repeat("s", 432)
	if err := q.Hint(t.Context(), r); err != nil {
		t.Fatalf("exact limit must be admitted: %v", err)
	}
	r.Identity.SessionID = strings.Repeat("s", 433)
	err := q.Hint(t.Context(), r)
	if !errors.Is(err, ErrKeyLength) || !errors.Is(err, ErrQuota) || classify(err, nil) != FailureQuota {
		t.Fatalf("err=%v class=%s", err, classify(err, nil))
	}
	if n := sourceCount(t, q); n != 1 {
		t.Fatalf("rows=%d, want only the admitted one", n)
	}
	if err := q.HintBatch(t.Context(), []Request{r}); !errors.Is(err, ErrKeyLength) {
		t.Fatalf("batch err=%v", err)
	}
}
