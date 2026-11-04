package adapters

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// The live-writer property tests prove the guarantee documented on
// BackupSQLiteAuthorized: while another connection or process inserts, grows the
// WAL, checkpoints, closes (deleting the WAL) and recreates it, every backup either
// fails with a typed changed-source error or yields a consistent snapshot. A
// consistent snapshot passes integrity_check and holds exactly the contiguous
// committed prefix 1..k of the writer's monotonically numbered rows. The backup
// takes no lock on the source, so the writer must also finish with an intact
// database that lost no commit.

const (
	liveWriterPathEnv = "MEMORY_WAL_LIVE_WRITER_DB"
	liveWriterSeedEnv = "MEMORY_WAL_LIVE_WRITER_SEED"
)

// liveBody is a deterministic row payload spanning one to several pages, so a torn
// or partial row is detectable and the WAL grows by multiple frames per commit.
func liveBody(n int) string {
	return strings.Repeat(string(rune('a'+n%26)), 50+(n*977)%5000) + strconv.Itoa(n)
}

type liveWriter struct {
	path                 string
	autocheckpoint       int
	db                   *sql.DB
	n                    int
	attempted, committed atomic.Int64
	report               func(kind string, n int)
}

func (w *liveWriter) open() error {
	db, err := sql.Open("sqlite", w.path)
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	for _, q := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=OFF",
		fmt.Sprintf("PRAGMA wal_autocheckpoint=%d", w.autocheckpoint),
		"CREATE TABLE IF NOT EXISTS log(n INTEGER PRIMARY KEY, body TEXT NOT NULL)",
	} {
		if _, err := db.ExecContext(context.Background(), q); err != nil {
			return errors.Join(err, db.Close())
		}
	}
	w.db = db
	return nil
}

// closeClean closes the last connection, which checkpoints and deletes the WAL.
func (w *liveWriter) closeClean() error {
	if w.db == nil {
		return nil
	}
	err := w.db.Close()
	w.db = nil
	return err
}

func (w *liveWriter) ensureOpen() error {
	if w.db != nil {
		return nil
	}
	return w.open()
}

func (w *liveWriter) insert(count int) error {
	if err := w.ensureOpen(); err != nil {
		return err
	}
	for range count {
		next := w.n + 1
		w.attempted.Store(int64(next))
		if w.report != nil {
			w.report("a", next)
		}
		if _, err := w.db.ExecContext(context.Background(), "INSERT INTO log(n, body) VALUES(?, ?)", next, liveBody(next)); err != nil {
			return err
		}
		w.n = next
		w.committed.Store(int64(next))
		if w.report != nil {
			w.report("c", next)
		}
	}
	return nil
}

func (w *liveWriter) checkpoint(mode string) error {
	if err := w.ensureOpen(); err != nil {
		return err
	}
	var busy, logFrames, checkpointed int
	return w.db.QueryRowContext(context.Background(), "PRAGMA wal_checkpoint("+mode+")").Scan(&busy, &logFrames, &checkpointed)
}

// step performs one seeded writer operation: inserts, PASSIVE/FULL/TRUNCATE
// checkpoints, or a clean close that removes the WAL until the next operation.
func (w *liveWriter) step(rng *rand.Rand) error {
	switch op := rng.IntN(10); {
	case op < 6:
		return w.insert(1 + rng.IntN(20))
	case op < 9:
		return w.checkpoint([]string{"PASSIVE", "FULL", "TRUNCATE"}[op-6])
	default:
		return w.closeClean()
	}
}

// verifyLiveDB checks integrity and returns k for a database holding exactly rows
// 1..k with their expected bodies. The file must have no live WAL.
func verifyLiveDB(ctx context.Context, path string) (int, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?immutable=1")
	if err != nil {
		return 0, err
	}
	defer func() { _ = db.Close() }()
	var verdict string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&verdict); err != nil {
		return 0, err
	}
	if verdict != "ok" {
		return 0, fmt.Errorf("integrity_check: %s", verdict)
	}
	rows, err := db.QueryContext(ctx, "SELECT n, body FROM log ORDER BY n")
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	count := 0
	for rows.Next() {
		var n int
		var body string
		if err := rows.Scan(&n, &body); err != nil {
			return 0, err
		}
		count++
		if n != count || body != liveBody(n) {
			return 0, fmt.Errorf("row %d at position %d is torn or out of order", n, count)
		}
	}
	return count, rows.Err()
}

type liveTally struct {
	success, changed int
	other            map[string]int
}

func (l *liveTally) add(err error) {
	switch {
	case err == nil:
		l.success++
	case isChangedSource(err):
		l.changed++
	default:
		if l.other == nil {
			l.other = make(map[string]int)
		}
		l.other[err.Error()]++
	}
}

func (l *liveTally) require(t *testing.T, name string) {
	t.Helper()
	t.Logf("%s outcomes: success=%d changed-source=%d other=%v", name, l.success, l.changed, l.other)
	if len(l.other) != 0 {
		t.Errorf("%s: backup failed with an untyped error: %v", name, l.other)
	}
	if l.success == 0 || l.changed == 0 {
		t.Errorf("%s: vacuous run, success=%d changed-source=%d", name, l.success, l.changed)
	}
}

// liveBackup runs one backup, calling hook at every observation boundary and
// every page step. A successful backup is verified; its row count is returned.
func liveBackup(t *testing.T, src, dir string, serial int, hook func()) (int, error) {
	t.Helper()
	dest := filepath.Join(dir, fmt.Sprintf("out-%d.db", serial))
	if err := os.WriteFile(dest, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(dest) }()
	err := BackupSQLiteAuthorized(t.Context(), src, dest, 1<<30,
		func() error { hook(); return nil },
		SQLiteAuthorizedOptions{
			Authorize:    func(context.Context, SQLiteAuthorization) error { hook(); return nil },
			ObserveInput: func(SQLiteAuthorization) error { hook(); return nil },
		})
	if err != nil {
		return 0, err
	}
	k, verr := verifyLiveDB(t.Context(), dest)
	if verr != nil {
		t.Fatalf("successful backup is not a consistent snapshot: %v", verr)
	}
	return k, nil
}

func liveDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteLiveWriterHookInterleaving(t *testing.T) {
	var tally liveTally
	for seed := uint64(1); seed <= 24; seed++ {
		runLiveHookSeed(t, seed, &tally)
	}
	tally.require(t, "hook-interleaved")
}

func runLiveHookSeed(t *testing.T, seed uint64, tally *liveTally) {
	t.Helper()
	dir := liveDir(t)
	src := filepath.Join(dir, "source.db")
	w := &liveWriter{path: src, autocheckpoint: []int{0, 4, 1000}[seed%3]}
	must(t, w.insert(5))
	rng := rand.New(rand.NewPCG(seed, 0xa11ce))
	for serial := range 10 {
		// One writer operation at a seeded boundary; some backups see none.
		target, calls := rng.IntN(16), 0
		hook := func() {
			if calls == target {
				must(t, w.step(rng))
			}
			calls++
		}
		before := w.committed.Load()
		k, err := liveBackup(t, src, dir, serial, hook)
		tally.add(err)
		if err == nil && (int64(k) != before || int64(k) != w.committed.Load()) {
			t.Fatalf("seed %d: snapshot holds %d rows, writer committed %d..%d during an unchanged backup", seed, k, before, w.committed.Load())
		}
	}
	// A paused writer, with the WAL present and then absent, is captured exactly.
	for _, clean := range []bool{false, true} {
		if clean {
			must(t, w.closeClean())
		} else {
			must(t, w.insert(1))
		}
		k, err := liveBackup(t, src, dir, 100, func() {})
		if err != nil || int64(k) != w.committed.Load() {
			t.Fatalf("seed %d: paused writer (clean=%v) captured %d rows, err=%v, committed %d", seed, clean, k, err, w.committed.Load())
		}
	}
	must(t, w.closeClean())
	if k, err := verifyLiveDB(t.Context(), src); err != nil || int64(k) != w.committed.Load() {
		t.Fatalf("seed %d: writer database lost commits: %d of %d, err=%v", seed, k, w.committed.Load(), err)
	}
}

func TestSQLiteLiveWriterGoroutine(t *testing.T) {
	var tally liveTally
	for seed := uint64(1); seed <= 4; seed++ {
		runLiveGoroutineSeed(t, seed, &tally)
	}
	tally.require(t, "goroutine-writer")
}

func runLiveGoroutineSeed(t *testing.T, seed uint64, tally *liveTally) {
	t.Helper()
	dir := liveDir(t)
	src := filepath.Join(dir, "source.db")
	w := &liveWriter{path: src, autocheckpoint: []int{0, 4, 1000, 16}[seed%4]}
	must(t, w.insert(5))
	stop := make(chan struct{})
	done := make(chan error, 1)
	var gate sync.Mutex // held by the test to pause the writer between operations
	go func() {
		rng := rand.New(rand.NewPCG(seed, 0xfeed))
		for {
			select {
			case <-stop:
				done <- w.closeClean()
				return
			default:
			}
			gate.Lock()
			err := w.step(rng)
			gate.Unlock()
			if err != nil {
				done <- err
				return
			}
			runtime.Gosched()
		}
	}()
	for serial := range 40 {
		// Every fourth backup runs with the writer paused between operations, so a
		// consistent capture is guaranteed and must equal the committed state.
		paused := serial%4 == 3
		if paused {
			gate.Lock()
		}
		before := w.committed.Load()
		k, err := liveBackup(t, src, dir, serial, func() {})
		after := w.attempted.Load()
		if paused {
			gate.Unlock()
			if err != nil || int64(k) != before || before != after {
				t.Fatalf("seed %d: paused writer captured %d rows (err=%v), committed %d, attempted %d", seed, k, err, before, after)
			}
		}
		tally.add(err)
		if err == nil && (int64(k) < before || int64(k) > after) {
			t.Fatalf("seed %d: snapshot of %d rows outside writer window %d..%d", seed, k, before, after)
		}
	}
	close(stop)
	must(t, <-done)
	if k, err := verifyLiveDB(t.Context(), src); err != nil || int64(k) != w.committed.Load() {
		t.Fatalf("seed %d: writer database lost commits: %d of %d, err=%v", seed, k, w.committed.Load(), err)
	}
}

// TestSQLiteLiveWriterChild is the separate writer process. It runs only when the
// parent test re-executes this binary, and writes until its stdin closes.
func TestSQLiteLiveWriterChild(t *testing.T) {
	path := os.Getenv(liveWriterPathEnv)
	if path == "" {
		t.Skip("helper process for TestSQLiteLiveWriterProcess")
	}
	seed, err := strconv.ParseUint(os.Getenv(liveWriterSeedEnv), 10, 64)
	must(t, err)
	w := &liveWriter{path: path, autocheckpoint: []int{0, 4, 1000, 16}[seed%4]}
	w.report = func(kind string, n int) { _, _ = fmt.Fprintf(os.Stdout, "%s %d\n", kind, n) }
	must(t, w.insert(5))
	cmds := make(chan string)
	go func() {
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			cmds <- sc.Text()
		}
		close(cmds)
	}()
	rng := rand.New(rand.NewPCG(seed, 0xc41d))
	for {
		select {
		case cmd, ok := <-cmds:
			if !ok {
				must(t, w.closeClean())
				_, _ = fmt.Fprintf(os.Stdout, "done %d\n", w.n)
				return
			}
			if cmd == "pause" {
				// Between operations DB and WAL are frozen until "resume" arrives.
				_, _ = fmt.Fprintln(os.Stdout, "paused")
				if _, ok := <-cmds; !ok {
					must(t, w.closeClean())
					_, _ = fmt.Fprintf(os.Stdout, "done %d\n", w.n)
					return
				}
			}
		default:
			must(t, w.step(rng))
		}
	}
}

type liveChild struct {
	cmd                  *exec.Cmd
	stdin                io.WriteCloser
	attempted, committed atomic.Int64
	done                 atomic.Int64
	ready, eof           chan struct{}
	paused               chan struct{}
}

func startLiveChild(t *testing.T, path string, seed uint64) *liveChild {
	t.Helper()
	c := &liveChild{ready: make(chan struct{}), eof: make(chan struct{}), paused: make(chan struct{}, 1)}
	c.done.Store(-1)
	c.cmd = exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestSQLiteLiveWriterChild$")
	c.cmd.Env = append(os.Environ(), liveWriterPathEnv+"="+path, liveWriterSeedEnv+"="+strconv.FormatUint(seed, 10))
	c.cmd.Stderr = os.Stderr
	var err error
	c.stdin, err = c.cmd.StdinPipe()
	must(t, err)
	out, err := c.cmd.StdoutPipe()
	must(t, err)
	must(t, c.cmd.Start())
	var once sync.Once
	go func() {
		defer close(c.eof)
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			if sc.Text() == "paused" {
				c.paused <- struct{}{}
				continue
			}
			kind, num, ok := strings.Cut(sc.Text(), " ")
			n, err := strconv.ParseInt(num, 10, 64)
			if !ok || err != nil {
				continue
			}
			switch kind {
			case "a":
				c.attempted.Store(n)
			case "c":
				c.committed.Store(n)
				once.Do(func() { close(c.ready) })
			case "done":
				c.done.Store(n)
			}
		}
	}()
	select {
	case <-c.ready:
	case <-c.eof:
		t.Fatal("writer process exited before committing")
	}
	return c
}

// pause returns once the writer process is idle between operations.
func (c *liveChild) pause(t *testing.T) {
	t.Helper()
	_, err := io.WriteString(c.stdin, "pause\n")
	must(t, err)
	<-c.paused
}

func (c *liveChild) resume(t *testing.T) {
	t.Helper()
	_, err := io.WriteString(c.stdin, "resume\n")
	must(t, err)
}

func (c *liveChild) finish(t *testing.T) {
	t.Helper()
	must(t, c.stdin.Close())
	<-c.eof
	must(t, c.cmd.Wait())
}

func TestSQLiteLiveWriterProcess(t *testing.T) {
	var tally liveTally
	for seed := uint64(1); seed <= 3; seed++ {
		dir := liveDir(t)
		src := filepath.Join(dir, "source.db")
		c := startLiveChild(t, src, seed)
		var ks []int
		for serial := range 40 {
			// Every fourth backup stops the writer process mid-stream (pause handshake), which
			// freezes DB+WAL wherever they are, so that capture must succeed.
			paused := serial%4 == 3
			if paused {
				c.pause(t)
			}
			before := c.committed.Load()
			k, err := liveBackup(t, src, dir, serial, func() {})
			if paused {
				c.resume(t)
				if err != nil {
					t.Fatalf("seed %d: stopped writer process was not captured: %v", seed, err)
				}
			}
			tally.add(err)
			if err == nil {
				if int64(k) < before {
					t.Fatalf("seed %d: snapshot of %d rows is older than committed %d", seed, k, before)
				}
				ks = append(ks, k)
			}
		}
		c.finish(t)
		for _, k := range ks {
			if int64(k) > c.done.Load() {
				t.Fatalf("seed %d: snapshot of %d rows exceeds the writer's %d", seed, k, c.done.Load())
			}
		}
		k, err := verifyLiveDB(t.Context(), src)
		if err != nil || c.done.Load() < 0 || int64(k) != c.done.Load() || int64(k) < c.committed.Load() {
			t.Fatalf("seed %d: writer process database has %d rows, reported %d, err=%v", seed, k, c.done.Load(), err)
		}
	}
	tally.require(t, "process-writer")
}

func isChangedSource(err error) bool { return errors.Is(err, ErrSQLiteSourceChanged) }

// A rewrite that keeps size and mtime is caught only by the final rehash: a writer
// bumps the header change counter in place during the backup and restores mtime.
func TestSQLiteLiveWriterSameSizeSameMtimeRewriteIsTyped(t *testing.T) {
	src := cleanClosedWALDB(t)
	dest := filepath.Join(filepath.Dir(src), "prepared.db")
	must(t, os.WriteFile(dest, nil, 0o600))
	st, err := os.Stat(src)
	must(t, err)
	rewritten := false
	reserve := func() error {
		if rewritten {
			return nil
		}
		rewritten = true
		f, err := os.OpenFile(src, os.O_RDWR, 0)
		must(t, err)
		_, err = f.WriteAt([]byte{0xde, 0xad, 0xbe, 0xef}, 24) // file change counter
		must(t, errors.Join(err, f.Close()))
		must(t, os.Chtimes(src, st.ModTime(), st.ModTime()))
		return nil
	}
	err = BackupSQLiteAuthorized(t.Context(), src, dest, 1<<30, reserve,
		SQLiteAuthorizedOptions{Authorize: func(context.Context, SQLiteAuthorization) error { return nil }})
	if !errors.Is(err, ErrSQLiteSourceChanged) || !strings.Contains(err.Error(), "bytes") {
		t.Fatalf("in-place rewrite with restored mtime: %v", err)
	}
}
