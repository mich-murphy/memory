package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/mich-murphy/memory/internal/publication"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

func TestCaptureExportByteLimitsDirectAndConfigured(t *testing.T) {
	c, config := runtimeFixture(t)
	b := publication.NewLocal(c.Store)
	if err := publication.WriteArchive(t.Context(), b, publication.Archive{Contract: publication.Contract, ArchiveID: c.ArchiveID}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(c.Home, ".claude", "projects", "test", "source.jsonl")
	runtimeWrite(t, path)
	q, err := sessioncapture.Open(t.Context(), c.Spool, sessioncapture.Options{ArchiveID: c.ArchiveID, MaxSpoolBytes: c.MaxSpoolBytes, CheckPolicy: runtimePolicy(c)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := q.Close(); err != nil {
			t.Error(err)
		}
	}()
	id := publication.Identity{ArchiveID: c.ArchiveID, MachineID: c.MachineID, Agent: "claude", SessionID: "session"}
	m, err := q.Capture(t.Context(), sessioncapture.Request{Identity: id, SourceID: "main", Role: "main", Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Deliver(t.Context(), b, id); err != nil {
		t.Fatal(err)
	}
	size := m.Sources[0].Object.Size
	var out bytes.Buffer
	io := IO{Stdout: &out, Stderr: &out}
	for _, backendArgs := range [][]string{
		{"--config", config},
		{"--store", c.Store, "--archive", c.ArchiveID, "--machine", c.MachineID},
	} {
		for _, limit := range []int64{size - 1, size} {
			dest := filepath.Join(t.TempDir(), "export")
			args := append([]string{"export", "--agent", id.Agent, "--session", id.SessionID, "--output", dest, "--max-bytes", strconv.FormatInt(limit, 10)}, backendArgs...)
			_, err := runCapture(t.Context(), args, io)
			if limit < size {
				if !errors.Is(err, publication.ErrExportBudget) {
					t.Fatal("budget error lost", err)
				}
				if _, err := os.Stat(dest); !os.IsNotExist(err) {
					t.Fatal("budget failure created output")
				}
			} else if err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, value := range []string{"-1", "0", "1099511627777", "9223372036854775808"} {
		code, err := runCapture(t.Context(), []string{"export", "--config", "not-read", "--max-bytes", value}, io)
		if code != exitUsage || err == nil {
			t.Fatal("invalid byte limit not usage error")
		}
	}
	flags, err := parseCaptureFlags("export", []string{"--archive", "test"}, io)
	if err != nil || flags.maxBytes != publication.DefaultExportMaxBytes {
		t.Fatal("wrong default byte limit", err)
	}
}
