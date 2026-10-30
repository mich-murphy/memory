package sessioncapture

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestPolicyCallerCancellationIsNotPermanentExclusion(t *testing.T) {
	for _, at := range []int{1, 2} {
		t.Run(string(rune('0'+at)), func(t *testing.T) {
			dir, options, request := setup(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			options.CheckPolicy = func(context.Context, Request) error {
				calls++
				if calls == at {
					cancel()
					return ctx.Err()
				}
				return nil
			}
			q := open(t, dir, options)
			_, err := q.check(ctx, evidence{Request: request, Identities: []string{request.Path}}, false)
			if !errors.Is(err, context.Canceled) || errors.Is(err, ErrPolicy) {
				t.Fatalf("caller cancellation became a permanent policy error: %v", err)
			}
			status, err := q.Status(t.Context())
			if err != nil || len(status) != 0 {
				t.Fatal("cancelled policy advanced byte ownership")
			}
		})
	}
}

func TestPolicyJoinedFailureRemainsDeniedWithoutExposingMessage(t *testing.T) {
	dir, options, request := setup(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	options.CheckPolicy = func(context.Context, Request) error {
		cancel()
		return errors.Join(ctx.Err(), errors.New("synthetic-sensitive-policy-diagnostic"))
	}
	q := open(t, dir, options)
	_, err := q.check(ctx, evidence{Request: request}, false)
	if !errors.Is(err, ErrPolicy) || pureCancellation(err, ctx.Err()) || strings.Contains(err.Error(), "synthetic-sensitive") {
		t.Fatalf("joined policy failure hidden or exposed: %v", err)
	}
}
