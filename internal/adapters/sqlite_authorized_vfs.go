package adapters

// This adapter deliberately depends on the pinned modernc SQLite ABI. It wraps
// only its own uniquely registered fs VFS; no default/global VFS is modified.
import (
	"errors"
	"io/fs"
	"sync"
	"unsafe"

	"modernc.org/libc"
	sqlite3 "modernc.org/sqlite/lib"
	"modernc.org/sqlite/vfs"
)

type authorizedVFS struct {
	fs      *vfs.FS
	ptr     uintptr
	open    uintptr
	methods uintptr
}

var authorizedVFSRegistry = struct {
	sync.Mutex
	entries map[uintptr]*authorizedVFS
}{entries: make(map[uintptr]*authorizedVFS)}

//nolint:govet // SQLite ABI pointers refer to libc allocations, not Go allocations.
func newAuthorizedVFS(files fs.FS) (string, *authorizedVFS, error) {
	name, registered, err := vfs.New(files)
	if err != nil {
		return "", nil, err
	}
	tls := libc.NewTLS()
	defer tls.Close()
	cname, err := libc.CString(name)
	if err != nil {
		_ = registered.Close()
		return "", nil, err
	}
	defer libc.Xfree(tls, cname)
	ptr := sqlite3.Xsqlite3_vfs_find(tls, cname)
	if ptr == 0 {
		_ = registered.Close()
		return "", nil, errors.New("authorized sqlite VFS missing")
	}
	raw := (*sqlite3.Tsqlite3_vfs)(unsafe.Pointer(ptr))
	methods := libc.Xcalloc(tls, 1, uint64(unsafe.Sizeof(sqlite3.Tsqlite3_io_methods{})))
	if methods == 0 {
		_ = registered.Close()
		return "", nil, errors.New("sqlite methods allocation failed")
	}
	a := &authorizedVFS{fs: registered, ptr: ptr, open: raw.FxOpen, methods: methods}
	authorizedVFSRegistry.Lock()
	authorizedVFSRegistry.entries[ptr] = a
	authorizedVFSRegistry.Unlock()
	raw.FxOpen = *(*uintptr)(unsafe.Pointer(&struct {
		f func(*libc.TLS, uintptr, uintptr, uintptr, int32, uintptr) int32
	}{authorizedVFSOpen}))
	raw.FxDelete = *(*uintptr)(unsafe.Pointer(&struct {
		f func(*libc.TLS, uintptr, uintptr, int32) int32
	}{authorizedVFSDelete}))
	return name, a, nil
}

func (a *authorizedVFS) Close() error {
	if err := a.fs.Close(); err != nil {
		return err
	}
	authorizedVFSRegistry.Lock()
	delete(authorizedVFSRegistry.entries, a.ptr)
	authorizedVFSRegistry.Unlock()
	tls := libc.NewTLS()
	libc.Xfree(tls, a.methods)
	tls.Close()
	return nil
}

//nolint:govet // SQLite callback arguments and methods are libc allocation addresses.
func authorizedVFSOpen(tls *libc.TLS, pvfs, name, file uintptr, flags int32, out uintptr) int32 {
	authorizedVFSRegistry.Lock()
	defer authorizedVFSRegistry.Unlock()
	a := authorizedVFSRegistry.entries[pvfs]
	if a == nil {
		return sqlite3.SQLITE_CANTOPEN
	}
	if flags&(sqlite3.SQLITE_OPEN_MAIN_DB|sqlite3.SQLITE_OPEN_WAL) == 0 {
		return sqlite3.SQLITE_CANTOPEN
	}
	rc := (*(*func(*libc.TLS, uintptr, uintptr, uintptr, int32, uintptr) int32)(unsafe.Pointer(&a.open)))(tls, pvfs, name, file, flags, out)
	if rc != sqlite3.SQLITE_OK {
		return rc
	}
	if out != 0 {
		*(*int32)(unsafe.Pointer(out)) = sqlite3.SQLITE_OPEN_READONLY
	}
	raw := (*sqlite3.Tsqlite3_file)(unsafe.Pointer(file))
	methods := (*sqlite3.Tsqlite3_io_methods)(unsafe.Pointer(a.methods))
	if methods.FxClose == 0 {
		*methods = *(*sqlite3.Tsqlite3_io_methods)(unsafe.Pointer(raw.FpMethods))
		methods.FxWrite = *(*uintptr)(unsafe.Pointer(&struct {
			f func(*libc.TLS, uintptr, uintptr, int32, int64) int32
		}{authorizedVFSWrite}))
		methods.FxTruncate = *(*uintptr)(unsafe.Pointer(&struct {
			f func(*libc.TLS, uintptr, int64) int32
		}{authorizedVFSTruncate}))
		methods.FxSync = *(*uintptr)(unsafe.Pointer(&struct {
			f func(*libc.TLS, uintptr, int32) int32
		}{authorizedVFSSync}))
	}
	raw.FpMethods = a.methods
	return rc
}

func authorizedVFSWrite(*libc.TLS, uintptr, uintptr, int32, int64) int32 {
	return sqlite3.SQLITE_READONLY
}

func authorizedVFSTruncate(*libc.TLS, uintptr, int64) int32 { return sqlite3.SQLITE_READONLY }

func authorizedVFSSync(*libc.TLS, uintptr, int32) int32 { return sqlite3.SQLITE_READONLY }

func authorizedVFSDelete(*libc.TLS, uintptr, uintptr, int32) int32 { return sqlite3.SQLITE_READONLY }
