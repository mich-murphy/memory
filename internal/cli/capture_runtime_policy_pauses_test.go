package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestPolicyBarrierInspectionAndNonceGuardedExplicitClear(t *testing.T) {
	c, config := runtimeFixture(t)
	var output bytes.Buffer
	io := IO{Stdout: &output, Stderr: &output}
	if err := recordUnresolvedNativePolicy(t.Context(), c, "claude"); err != nil {
		t.Fatal(err)
	}
	markers, err := nativePolicyPauses(c)
	if err != nil || len(markers) != 1 {
		t.Fatal("barrier inspection failed")
	}
	old := markers[0].Nonce
	if _, err := runCaptureRuntime(t.Context(), "policy-pauses", []string{"--config", config}, io); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), old) || strings.Contains(output.String(), c.Home) {
		t.Fatal("policy status omitted nonce or exposed input paths")
	}
	if err := recordUnresolvedNativePolicy(t.Context(), c, "claude"); err != nil {
		t.Fatal(err)
	}
	if err := clearUnresolvedNativePolicy(t.Context(), c, "claude", old); err == nil {
		t.Fatal("stale clear erased newer disabled observation")
	}
	markers, err = nativePolicyPauses(c)
	if err != nil || len(markers) != 1 || markers[0].Count != 2 {
		t.Fatal("new observation lost")
	}
	base := []string{"--config", config, "--action", "clear", "--agent", "claude", "--nonce", markers[0].Nonce, "--apply"}
	if _, err := runCaptureRuntime(t.Context(), "policy-pauses", base, io); err == nil {
		t.Fatal("clear lacked explicit reconciliation acknowledgement")
	}
	if runtimeUnresolvedNativePolicy(c, "claude") == nil {
		t.Fatal("failed clear lifted barrier")
	}
	if _, err := runCaptureRuntime(t.Context(), "policy-pauses", append(base, "--acknowledge-unresolved-opt-outs"), io); err != nil {
		t.Fatal(err)
	}
	if err := runtimeUnresolvedNativePolicy(c, "claude"); err != nil {
		t.Fatal(err)
	}
	if _, err := runCaptureRuntime(t.Context(), "policy-pauses", []string{"--config", config, "--apply"}, io); err == nil {
		t.Fatal("read-only list accepted mutation flags")
	}
}

func TestRuntimeEnrichmentFlagsRejectInvalidCoordinates(t *testing.T) {
	var out bytes.Buffer
	io := IO{Stdout: &out, Stderr: &out}
	for _, args := range [][]string{{"--config", "fixture", "--work-id", "bad"}, {"--config", "fixture"}} {
		if _, err := parseRuntimeFlags("enrichment-replay", args, io); err == nil {
			t.Fatal("invalid enrichment coordinate accepted")
		}
	}
	for _, args := range [][]string{{"--config", "fixture", "--limit", "0"}, {"--config", "fixture", "--limit", "257"}, {"--config", "fixture", "--after-id", "bad"}} {
		if _, err := parseRuntimeFlags("enrichment-status", args, io); err == nil {
			t.Fatal("invalid enrichment page accepted")
		}
	}
	if _, err := parseRuntimeFlags("enrichment-replay", []string{"--config", "fixture", "--work-id", strings.Repeat("a", 64)}, io); err != nil {
		t.Fatal(err)
	}
}
