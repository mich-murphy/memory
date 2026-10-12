// Package logging writes the collector's log: a small rotating file in the state dir, plus
// warnings on a terminal.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
)

const (
	maxBytes = 1_000_000
	backups  = 3
)

// FileName is the log's name in the state dir.
const FileName = "agent-sessions.log"

// Open returns a logger writing INFO and above to the log file and, unless quiet, WARN and
// above to stderr when it is a terminal. The returned function closes the file.
func Open(stateDir string, quiet bool) (*slog.Logger, func(), error) {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, nil, fmt.Errorf("log: %w", err)
	}
	path := filepath.Join(stateDir, FileName)
	rotate(path)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("log: %w", err)
	}
	handlers := []slog.Handler{newHandler(f, slog.LevelInfo).WithAttrs([]slog.Attr{slog.Int("pid", os.Getpid())})}
	if !quiet && isTerminal(os.Stderr) {
		handlers = append(handlers, newHandler(os.Stderr, slog.LevelWarn))
	}
	return slog.New(slog.NewMultiHandler(handlers...)), func() { _ = f.Close() }, nil // appends are already written
}

func newHandler(w io.Writer, level slog.Level) slog.Handler {
	return slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})
}

// rotate keeps the log under maxBytes by shifting it to .1, .2, .3. Several processes may
// share the file; a rotation racing another is harmless, since each only appends.
func rotate(path string) {
	st, err := os.Stat(path)
	if err != nil || st.Size() < maxBytes {
		return
	}
	for i := backups - 1; i >= 1; i-- {
		_ = os.Rename(path+"."+strconv.Itoa(i), path+"."+strconv.Itoa(i+1)) // missing older logs are fine
	}
	_ = os.Rename(path, path+".1") // if this fails the log just grows a little longer
}

func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}
