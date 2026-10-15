package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestAcceptanceAssignmentSyntaxNeverReachesArchiveOrLogs(t *testing.T) {
	r := newAcceptanceRun(t)
	root := filepath.Join(r.home, ".pi", "agent")
	fixtures := []struct{ name, body, literal string }{
		{"fishQuotedFlags", "set '-gx' 'API_KEY' review-fish-quoted-flags-secret", "review-fish-quoted-flags-secret"},
		{"fishQuotedLongFlags", `set '--global' "--export" API_KEY review-fish-quoted-long-secret`, "review-fish-quoted-long-secret"},
		{"fishQuotedCommand", "'set' -gx API_KEY review-fish-quoted-command-secret", "review-fish-quoted-command-secret"},
		{"fishJoinedExport", "ex'port' API_KEY=review-fish-joined-export-secret", "review-fish-joined-export-secret"},
		{"fishQuotedName", "set -gx 'API_KEY' review-fish-name-secret", "review-fish-name-secret"},
		{"fishReordered", "set -xg API_KEY review-fish-reordered-secret", "review-fish-reordered-secret"},
		{"fishSeparate", "set -g -x API_KEY review-fish-separate-secret", "review-fish-separate-secret"},
		{"fishLong", "set --global --export API_KEY review-fish-long-secret", "review-fish-long-secret"},
		{"fishAndExport", "true;and export API_KEY=review-fish-and-secret", "review-fish-and-secret"},
		{"fishOrExport", "false;or export API_KEY=review-fish-or-secret", "review-fish-or-secret"},
		{"fishHash", "set -gx PASSWORD '#review-fish-secret'", "#review-fish-secret"},
		{"separator", "OTHER=ok;API_KEY=review-shell-secret", "review-shell-secret"},
		{"fishSeparator", "set -gx OTHER ok;set -gx API_KEY review-separated-secret", "review-separated-secret"},
		{"malformed", "OTHER=ok API_KEY=review-malformed-secret CMD='", "review-malformed-secret"},
		{"singleQuoted", "API_KEY='${REVIEW_QUOTED_TOKEN}'", "${REVIEW_QUOTED_TOKEN}"},
		{"escaped", `API_KEY=\$REVIEW_ESCAPED_TOKEN`, "$REVIEW_ESCAPED_TOKEN"},
		{"fishQuoted", "set -gx API_KEY '${REVIEW_FISH_TOKEN}'", "${REVIEW_FISH_TOKEN}"},
		{"fishEscaped", `set -gx API_KEY \$REVIEW_FISH_ESCAPED_TOKEN`, "$REVIEW_FISH_ESCAPED_TOKEN"},
	}
	var literals []string
	for _, fixture := range fixtures {
		for _, dir := range []string{root, filepath.Join(r.work, ".pi")} {
			r.write(filepath.Join(dir, "extensions", fixture.name+".sh"), []byte(fixture.body+"\n"))
		}
		literals = append(literals, fixture.literal)
	}
	reference := []byte("export API_KEY=\"${CONTROL_TOKEN}\"\n" +
		"set -gx PASSWORD $CONTROL_PASSWORD\n" +
		"set '-gx' 'API_KEY' $CONTROL_TOKEN\n" +
		"set '--global' \"--export\" API_KEY $CONTROL_TOKEN\n" +
		"'set' -gx API_KEY $CONTROL_TOKEN\n" +
		"ex'port' API_KEY=$CONTROL_TOKEN\n" +
		"true;'and' export API_KEY=fixture-value\n" +
		"ex'port' 'API_KEY=fixture-value'\n" +
		"set -gx 'API_KEY' $CONTROL_TOKEN\n" +
		"set -xg API_KEY $CONTROL_TOKEN\n" +
		"set -g -x API_KEY $CONTROL_TOKEN\n" +
		"set --global --export API_KEY $CONTROL_TOKEN\n" +
		"true;and export API_KEY=$CONTROL_TOKEN\n" +
		"false;or export API_KEY=$CONTROL_TOKEN\n" +
		"set -q API_KEY fixture-value\n" +
		"set -gx 'PASSWORD NAME' fixture-value\n" +
		"For example, set -xg API_KEY fixture-value\n")
	r.write(filepath.Join(root, "extensions", "reference.sh"), reference)
	r.write(filepath.Join(r.work, ".pi", "extensions", "reference.sh"), reference)
	r.write(filepath.Join(root, "sessions", "--acceptance--", acceptancePiName+".jsonl"), acceptancePiBody(r.work))
	cmd := exec.CommandContext(t.Context(), filepath.Join(binDir, "agent-sessions"), "backfill", "--agent", "pi")
	cmd.Env, cmd.Dir = r.env, r.work
	logs, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("isolated backfill: %v\n%s", err, logs)
	}
	storedLogs, err := os.ReadFile(filepath.Join(r.state, "agent-sessions.log"))
	if err != nil {
		t.Fatal(err)
	}
	logs = append(logs, storedLogs...)
	for _, literal := range literals {
		if bytes.Contains(logs, []byte(literal)) {
			t.Fatal("withheld synthetic literal reached CLI logs")
		}
	}
	r.absentLiterals(literals...)
	for _, fixture := range fixtures {
		if len(r.records("file", "pi", "extensions/"+fixture.name+".sh")) != 0 {
			t.Error("withheld assignment acquired a committed raw generation")
		}
	}
	if !bytes.Equal(r.content(r.latest("file", "pi", "extensions/reference.sh")), reference) {
		t.Error("supported pure references changed")
	}
	foundReason, foundSnapshotReference := false, false
	for key := range r.inventory() {
		body, err := r.object(key)
		if err != nil {
			t.Fatal(err)
		}
		foundReason = foundReason || bytes.Contains(body, []byte("literal_secret_assignment"))
		// Snapshot blobs are plain; raw file pieces are zstd objects.
		foundSnapshotReference = foundSnapshotReference || bytes.Equal(body, reference)
	}
	if !foundSnapshotReference {
		t.Error("snapshot did not preserve references and unrelated commands")
	}
	if !foundReason {
		t.Error("snapshot withholding did not archive the fixed skip reason")
	}
}
