package adapters

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"

	"github.com/mich-murphy/memory/internal/capturepolicy"
	"github.com/mich-murphy/memory/internal/safefile"
)

// SQLiteAuthorization is metadata only. Both members are observed before any
// callback. WAL identities include the original spelling as restrictive evidence;
// that spelling does not grant sibling authority to an exact-leaf DB alias.
type SQLiteAuthorization struct {
	OriginalPath string
	ResolvedPath string
	Identities   []string
	Info         fs.FileInfo
	Present      bool
}

// ErrSQLiteRollbackJournal identifies an unsupported recovery requirement. No
// source journal is opened or recovered by this readonly capture path.
var ErrSQLiteRollbackJournal = errors.New("sqlite authorized: rollback journal unsupported")

type SQLiteAuthorizedOptions struct {
	Authorize func(context.Context, SQLiteAuthorization) error
	// ObserveInput retains bounded private provenance. It runs after all authorization
	// callbacks succeed and before any source is opened or read. An error aborts capture.
	ObserveInput func(SQLiteAuthorization) error
	// ExpectedMain binds a caller's earlier authorization observation, when present.
	ExpectedMain fs.FileInfo
}

type sqliteObserved struct {
	meta   SQLiteAuthorization
	file   *os.File
	parent fs.FileInfo
	hash   [32]byte
}

// BackupSQLiteAuthorized accepts only a verified stable observation of DB+WAL.
// It is not an atomic snapshot of an uncooperative mutable producer: changes
// between observations that are restored byte-for-byte cannot be detected.
// dest must be a queue-owned private empty output, never a source pathname.
// The caller owns prepared-slot admission and cleanup on any error.
func BackupSQLiteAuthorized(ctx context.Context, src, dest string, limit int64, reserve func() error, options SQLiteAuthorizedOptions) (err error) {
	if options.Authorize == nil || reserve == nil || limit < 65536 || limit > 1<<30 {
		return errors.New("sqlite authorized: invalid options")
	}
	st, e := os.Lstat(dest)
	if e != nil {
		return e
	}
	if !st.Mode().IsRegular() || st.Size() != 0 || st.Mode().Perm()&0o077 != 0 {
		return errors.New("sqlite authorized: private empty output required")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	pair, err := observeSQLitePair(src, options.ExpectedMain)
	if err != nil {
		return err
	}
	if err := boundedSQLiteIdentities(pair); err != nil {
		return err
	}
	if err := rejectSQLiteJournal(ctx, pair[0].meta.ResolvedPath); err != nil {
		return err
	}
	if err := authorizeSQLitePair(ctx, pair, options); err != nil {
		return err
	}
	defer closeSQLitePair(&pair)
	pins, err := pinSQLitePair(ctx, &pair, limit)
	if err != nil {
		return err
	}
	if err := checkSQLitePair(ctx, pair, false); err != nil {
		return err
	}
	if err := backupSQLitePinned(ctx, pins, dest, limit, reserve); err != nil {
		return err
	}
	if err := checkSQLitePair(ctx, pair, true); err != nil {
		return err
	}
	return checkSQLitePair(ctx, pair, false)
}

func observeSQLitePair(src string, expected fs.FileInfo) ([2]sqliteObserved, error) {
	var pair [2]sqliteObserved
	r, err := capturepolicy.Resolve(src)
	if err != nil {
		return pair, err
	}
	st, err := os.Lstat(r.Path)
	if err != nil {
		return pair, err
	}
	if !st.Mode().IsRegular() || expected != nil && !os.SameFile(st, expected) {
		return pair, errors.New("sqlite authorized: main identity changed")
	}
	pair[0].meta = SQLiteAuthorization{OriginalPath: src, ResolvedPath: r.Path, Identities: r.Identities, Info: st, Present: true}
	wal := r.Path + "-wal"
	wr, we := capturepolicy.Resolve(wal)
	if we != nil && !errors.Is(we, fs.ErrNotExist) {
		return pair, we
	}
	// A symlink at the WAL leaf is allowed only after all identities authorize.
	originalWAL, oe := capturepolicy.Resolve(src + "-wal")
	if oe != nil && !errors.Is(oe, fs.ErrNotExist) {
		return pair, oe
	}
	ids := append(append([]string{src + "-wal"}, originalWAL.Identities...), wr.Identities...)
	if we == nil {
		wal = wr.Path
	}
	pair[1].meta = SQLiteAuthorization{OriginalPath: src + "-wal", ResolvedPath: wal, Identities: ids, Present: we == nil}
	if we == nil {
		st, err = os.Lstat(wal)
		if err != nil {
			return pair, err
		}
		if !st.Mode().IsRegular() {
			return pair, errors.New("sqlite authorized: nonregular WAL")
		}
		pair[1].meta.Info = st
	} else {
		if _, e := os.Lstat(wal); !errors.Is(e, fs.ErrNotExist) {
			return pair, errors.New("sqlite authorized: unresolved WAL entry")
		}
		// Only absence of the immediate canonical sidecar is accepted; no missing ancestor.
		pair[1].parent, err = os.Stat(filepath.Dir(wal))
		if err != nil {
			return pair, err
		}
	}
	return pair, nil
}

func hashSQLiteFile(ctx context.Context, f *os.File, size int64) ([32]byte, error) {
	var result [32]byte
	h := sha256.New()
	buf := make([]byte, 64<<10)
	for offset := int64(0); offset < size; {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		n, err := f.ReadAt(buf[:min(int64(len(buf)), size-offset)], offset)
		if n > 0 {
			_, _ = h.Write(buf[:n])
			offset += int64(n)
		}
		if err != nil {
			return result, err
		}
	}
	copy(result[:], h.Sum(nil))
	return result, nil
}

func checkSQLitePair(ctx context.Context, pair [2]sqliteObserved, rehash bool) error {
	if err := rejectSQLiteJournal(ctx, pair[0].meta.ResolvedPath); err != nil {
		return err
	}
	r, err := capturepolicy.Resolve(pair[0].meta.OriginalPath)
	if err != nil {
		return err
	}
	if r.Path != pair[0].meta.ResolvedPath {
		return errors.New("sqlite authorized: original identity changed")
	}
	if pair[1].meta.Present {
		w, e := capturepolicy.Resolve(pair[0].meta.ResolvedPath + "-wal")
		if e != nil {
			return e
		}
		if w.Path != pair[1].meta.ResolvedPath {
			return errors.New("sqlite authorized: WAL alias changed")
		}
	}
	for i := range pair {
		if err := checkSQLiteMember(ctx, &pair[i], rehash); err != nil {
			return err
		}
	}

	return ctx.Err()
}

type sqlitePinnedFS struct {
	ctx   context.Context
	files map[string]*os.File
}

func (p sqlitePinnedFS) Open(name string) (fs.File, error) {
	if err := p.ctx.Err(); err != nil {
		return nil, err
	}
	f := p.files[name]
	if f == nil {
		return nil, fs.ErrNotExist
	}
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	return &sqlitePinnedReader{SectionReader: io.NewSectionReader(f, 0, st.Size()), ctx: p.ctx, info: st}, nil
}

type sqlitePinnedReader struct {
	*io.SectionReader
	ctx  context.Context
	info fs.FileInfo
}

func (p *sqlitePinnedReader) Read(b []byte) (int, error) {
	if err := p.ctx.Err(); err != nil {
		return 0, err
	}
	return p.SectionReader.Read(b)
}
func (p *sqlitePinnedReader) Stat() (fs.FileInfo, error) { return p.info, nil }
func (p *sqlitePinnedReader) Close() error               { return nil }

func backupSQLitePinned(ctx context.Context, pins sqlitePinnedFS, dest string, limit int64, reserve func() error) (err error) {
	name, registered, err := newAuthorizedVFS(pins)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, registered.Close()) }()
	uri := "file:main.db?mode=ro&_pragma=locking_mode(EXCLUSIVE)&vfs=" + name
	if pins.files["main.db-wal"] == nil {
		// A cleanly closed WAL-mode database has no sidecar: the pinned main file
		// is the complete committed state and the WAL's absence is verified before
		// and after backup. immutable=1 stops SQLite opening a WAL it cannot create.
		// It is never used when a WAL is pinned, which would silently drop its rows.
		uri += "&immutable=1"
	}
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, conn.Close()) }()
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := boundedSQLiteDimensions(ctx, tx, limit); err != nil {
		return err
	}
	return conn.Raw(func(dc any) error {
		b, ok := dc.(backuper)
		if !ok {
			return errors.New("sqlite backup unavailable")
		}
		bk, err := b.NewBackup(dest)
		if err != nil {
			return err
		}
		return stepBoundedBackup(ctx, bk, dest, limit, reserve)
	})
}

// Rollback recovery would require writes and another independently authorized
// input. Fail closed, including empty journals and symlinks, rather than pretending
// that a stable dirty main file is a committed SQLite snapshot.
func rejectSQLiteJournal(ctx context.Context, main string) error {
	dir, err := safefile.OpenDirectory(ctx, filepath.Dir(main))
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	var st unix.Stat_t
	err = unix.Fstatat(int(dir.Fd()), filepath.Base(main)+"-journal", &st, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return err
	}
	return ErrSQLiteRollbackJournal
}

func closeSQLitePair(pair *[2]sqliteObserved) {
	for i := range pair {
		if pair[i].file != nil {
			_ = pair[i].file.Close()
		}
	}
}

func pinSQLitePair(ctx context.Context, pair *[2]sqliteObserved, limit int64) (sqlitePinnedFS, error) {
	pins := sqlitePinnedFS{ctx: ctx, files: make(map[string]*os.File)}
	var total int64
	for i := range pair {
		p := &pair[i]
		if !p.meta.Present {
			continue
		}
		var err error
		p.file, err = safefile.OpenResolved(ctx, p.meta.ResolvedPath, p.meta.Info)
		if err != nil {
			return pins, err
		}
		total += p.meta.Info.Size()
		if p.meta.Info.Size() < 0 || total > limit {
			return pins, errors.New("sqlite authorized: source pair exceeds limit")
		}
		p.hash, err = hashSQLiteFile(ctx, p.file, p.meta.Info.Size())
		if err != nil {
			return pins, err
		}
		name := "main.db"
		if i == 1 {
			name += "-wal"
		}
		pins.files[name] = p.file
	}

	return pins, nil
}

func checkSQLiteAbsence(ctx context.Context, p *sqliteObserved) error {
	dir, err := safefile.OpenDirectory(ctx, filepath.Dir(p.meta.ResolvedPath))
	if err != nil {
		return err
	}
	st, e := dir.Stat()
	var stat unix.Stat_t
	if e == nil && !os.SameFile(st, p.parent) {
		e = errors.New("sqlite authorized: sidecar parent changed")
	}
	if e == nil {
		e = unix.Fstatat(int(dir.Fd()), filepath.Base(p.meta.ResolvedPath), &stat, unix.AT_SYMLINK_NOFOLLOW)
		if errors.Is(e, unix.ENOENT) {
			e = nil
		} else if e == nil {
			e = errors.New("sqlite authorized: WAL appeared")
		}
	}
	_ = dir.Close()
	if e != nil {
		return e
	}

	return nil
}

func checkSQLiteMember(ctx context.Context, p *sqliteObserved, rehash bool) error {
	if !p.meta.Present {
		return checkSQLiteAbsence(ctx, p)
	}
	fresh, err := safefile.OpenResolved(ctx, p.meta.ResolvedPath, p.meta.Info)
	if err != nil {
		return err
	}
	st, err := fresh.Stat()
	_ = fresh.Close()
	if err != nil {
		return err
	}
	pinned, err := p.file.Stat()
	if err != nil {
		return err
	}
	if st.Size() != p.meta.Info.Size() || pinned.Size() != st.Size() || !st.ModTime().Equal(p.meta.Info.ModTime()) || !pinned.ModTime().Equal(st.ModTime()) {
		return errors.New("sqlite authorized: source changed")
	}
	if rehash {
		digest, err := hashSQLiteFile(ctx, p.file, st.Size())
		if err != nil {
			return err
		}
		if digest != p.hash {
			return errors.New("sqlite authorized: source bytes changed")
		}
	}
	return nil
}

func boundedSQLiteIdentities(pair [2]sqliteObserved) error {
	count, bytes := 0, 0
	for i := range pair {
		for _, id := range pair[i].meta.Identities {
			count++
			bytes += len(id)
			if len(id) > 4096 || count > 256 || bytes > 64<<10 {
				return errors.New("sqlite authorized: identity metadata limit exceeded")
			}
		}
	}
	return nil
}

func authorizeSQLitePair(ctx context.Context, pair [2]sqliteObserved, options SQLiteAuthorizedOptions) error {
	for i := range pair {
		if new(capturepolicy.Policy).Excludes(pair[i].meta.Identities) {
			return errors.New("sqlite authorized: excluded identity")
		}
		// Do not expose mutable backing slices to authorization callbacks.
		meta := pair[i].meta
		meta.Identities = append([]string(nil), meta.Identities...)
		if err := options.Authorize(ctx, meta); err != nil {
			return err
		}
	}
	if options.ObserveInput != nil {
		for i := range pair {
			meta := pair[i].meta
			meta.Identities = append([]string(nil), meta.Identities...)
			if err := options.ObserveInput(meta); err != nil {
				return err
			}
		}
	}

	return nil
}
