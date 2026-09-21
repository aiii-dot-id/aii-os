package compressvfs

import (
	"errors"
	"fmt"
	"io"
	"runtime"
	"strconv"
	"sync"
	"unsafe"

	"modernc.org/libc"
	sqlite "modernc.org/sqlite/lib"
)

// .
// .
const Name = "aii-zstd"
const CreateParameter = "aii_compress"

// .
// .
// .
const FormatPragma = "aii_database_format"
const FormatSQLite = "sqlite"
const FormatZstd = "zstd"

// .
// .
// .
const CompactPragma = "aii_compress_compact"

// .
// .
// .
// .
func callback[T any](f T) uintptr { return *(*uintptr)(unsafe.Pointer(&f)) }
func function[T any](p uintptr) T { return *(*T)(unsafe.Pointer(&p)) }

// .
// .
// .
func nativeStruct[T any](p uintptr) *T {
	var value T
	return (*T)(unsafe.Pointer(unsafe.SliceData(libc.GoBytes(p, int(unsafe.Sizeof(value))))))
}

type openFunc = func(*libc.TLS, uintptr, uintptr, uintptr, int32, uintptr) int32
type closeFunc = func(*libc.TLS, uintptr) int32
type readWriteFunc = func(*libc.TLS, uintptr, uintptr, int32, int64) int32
type truncateFunc = func(*libc.TLS, uintptr, int64) int32
type flagFunc = func(*libc.TLS, uintptr, int32) int32
type pointerFunc = func(*libc.TLS, uintptr, uintptr) int32
type controlFunc = func(*libc.TLS, uintptr, int32, uintptr) int32
type shmMapFunc = func(*libc.TLS, uintptr, int32, int32, int32, uintptr) int32
type shmLockFunc = func(*libc.TLS, uintptr, int32, int32, int32) int32
type barrierFunc = func(*libc.TLS, uintptr)
type fetchFunc = func(*libc.TLS, uintptr, int64, int32, uintptr) int32
type unfetchFunc = func(*libc.TLS, uintptr, int64, uintptr) int32

// .
// .
// .
var (
	regOnce         sync.Once
	regErr          error
	parentVFS       uintptr
	ownVFS          uintptr
	methodsPointer  uintptr
	createParameter uintptr
	files           sync.Map
)

type vfsFile struct {
	native     uintptr
	methods    sqlite.Tsqlite3_io_methods
	compressed *container
	readOnly   bool
	// .
	// .
	tls *libc.TLS
}

type nativeError int32

func (e nativeError) Error() string { return fmt.Sprintf("native SQLite VFS error %d", int32(e)) }
func nativeResult(rc int32) error {
	if rc == sqlite.SQLITE_OK {
		return nil
	}
	if rc == sqlite.SQLITE_IOERR_SHORT_READ {
		return io.ErrUnexpectedEOF
	}
	return nativeError(rc)
}
func result(err error, fallback int32) int32 {
	if err == nil {
		return sqlite.SQLITE_OK
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return sqlite.SQLITE_IOERR_SHORT_READ
	}
	if errors.Is(err, errCorrupt) {
		return sqlite.SQLITE_CORRUPT
	}
	var native nativeError
	if errors.As(err, &native) {
		return int32(native)
	}
	return fallback
}

// .
// .
func Register() error {
	regOnce.Do(func() { regErr = register() })
	return regErr
}

func register() error {
	tls := libc.NewTLS()
	defer tls.Close()
	if rc := sqlite.Xsqlite3_initialize(tls); rc != sqlite.SQLITE_OK {
		return nativeError(rc)
	}
	parentVFS = sqlite.Xsqlite3_vfs_find(tls, 0)
	if parentVFS == 0 {
		return errors.New("native SQLite VFS is unavailable")
	}
	parent := *nativeStruct[sqlite.Tsqlite3_vfs](parentVFS)
	name, err := libc.CString(Name)
	if err != nil {
		return err
	}
	parameter, err := libc.CString(CreateParameter)
	if err != nil {
		libc.Xfree(tls, name)
		return err
	}
	vp := libc.Xcalloc(tls, 1, libc.Tsize_t(unsafe.Sizeof(parent)))
	mp := libc.Xcalloc(tls, 1, libc.Tsize_t(unsafe.Sizeof(sqlite.Tsqlite3_io_methods{})))
	if vp == 0 || mp == 0 {
		libc.Xfree(tls, vp)
		libc.Xfree(tls, mp)
		libc.Xfree(tls, name)
		libc.Xfree(tls, parameter)
		return nativeError(sqlite.SQLITE_NOMEM)
	}
	parent.FzName, parent.FpNext, parent.FxOpen = name, 0, callback(vfsOpen)
	*nativeStruct[sqlite.Tsqlite3_vfs](vp) = parent
	*nativeStruct[sqlite.Tsqlite3_io_methods](mp) = sqlite.Tsqlite3_io_methods{
		FiVersion: 3,
		FxClose:   callback(vfsClose), FxRead: callback(vfsRead), FxWrite: callback(vfsWrite),
		FxTruncate: callback(vfsTruncate), FxSync: callback(vfsSync), FxFileSize: callback(vfsSize),
		FxLock: callback(vfsLock), FxUnlock: callback(vfsUnlock), FxCheckReservedLock: callback(vfsReserved),
		FxFileControl: callback(vfsControl), FxSectorSize: callback(vfsSectorSize),
		FxDeviceCharacteristics: callback(vfsDevice), FxShmMap: callback(vfsShmMap),
		FxShmLock: callback(vfsShmLock), FxShmBarrier: callback(vfsShmBarrier), FxShmUnmap: callback(vfsShmUnmap),
		FxFetch: callback(vfsFetch), FxUnfetch: callback(vfsUnfetch),
	}
	ownVFS, methodsPointer, createParameter = vp, mp, parameter
	if rc := sqlite.Xsqlite3_vfs_register(tls, vp, 0); rc != sqlite.SQLITE_OK {
		libc.Xfree(tls, vp)
		libc.Xfree(tls, mp)
		libc.Xfree(tls, name)
		libc.Xfree(tls, parameter)
		return nativeError(rc)
	}
	return nil
}

func vfsOpen(tls *libc.TLS, _ uintptr, name, file uintptr, flags int32, outFlags uintptr) int32 {
	parent := nativeStruct[sqlite.Tsqlite3_vfs](parentVFS)
	open := function[openFunc](parent.FxOpen)
	if flags&sqlite.SQLITE_OPEN_MAIN_DB == 0 {
		// .
		return open(tls, parentVFS, name, file, flags, outFlags)
	}
	nativeStruct[sqlite.Tsqlite3_file](file).FpMethods = 0
	native := libc.Xcalloc(tls, 1, libc.Tsize_t(parent.FszOsFile))
	if native == 0 {
		return sqlite.SQLITE_NOMEM
	}
	if rc := open(tls, parentVFS, name, native, flags, outFlags); rc != sqlite.SQLITE_OK {
		libc.Xfree(tls, native)
		return rc
	}
	methods := *nativeStruct[sqlite.Tsqlite3_io_methods](nativeStruct[sqlite.Tsqlite3_file](native).FpMethods)
	f := &vfsFile{native: native, methods: methods, tls: tls, readOnly: flags&sqlite.SQLITE_OPEN_READONLY != 0}
	if outFlags != 0 {
		f.readOnly = *nativeStruct[int32](outFlags)&sqlite.SQLITE_OPEN_READONLY != 0
	}
	size, err := f.size()
	compressed := false
	if err == nil && size >= int64(len(fileMagic)) {
		header := make([]byte, len(fileMagic))
		err = f.read(header, 0)
		compressed = string(header) == fileMagic
	}
	if err == nil && size == 0 && name != 0 {
		compressed = sqlite.Xsqlite3_uri_boolean(tls, name, createParameter, 0) != 0
	}
	if err == nil && compressed {
		// .
		// .
		err = nativeResult(function[flagFunc](methods.FxLock)(tls, native, sqlite.SQLITE_LOCK_SHARED))
		if err == nil {
			sector := function[closeFunc](methods.FxSectorSize)(tls, native)
			if !f.readOnly && (sector <= 0 || sector > blockSize || blockSize%sector != 0) {
				err = fmt.Errorf("compressed format requires a native sector dividing %d bytes; got %d", blockSize, sector)
			} else {
				f.compressed, err = newContainer(f)
			}
			if unlock := nativeResult(function[flagFunc](methods.FxUnlock)(tls, native, sqlite.SQLITE_LOCK_NONE)); err == nil {
				err = unlock
			}
		}
	}
	if err != nil {
		if f.compressed != nil {
			f.compressed.close()
		}
		function[closeFunc](methods.FxClose)(tls, native)
		libc.Xfree(tls, native)
		return result(err, sqlite.SQLITE_CANTOPEN)
	}
	files.Store(file, f)
	nativeStruct[sqlite.Tsqlite3_file](file).FpMethods = methodsPointer
	return sqlite.SQLITE_OK
}

func state(tls *libc.TLS, file uintptr) *vfsFile {
	v, found := files.Load(file)
	if !found {
		return nil
	}
	f := v.(*vfsFile)
	f.tls = tls
	return f
}

// .
// .
// .
// .
const pendingByte = int64(0x40000000)
const lockGap = int64(blockSize)

func physicalOffset(offset int64) int64 {
	if offset >= pendingByte {
		return offset + lockGap
	}
	return offset
}

func (f *vfsFile) nativeIO(p []byte, offset int64, ptr uintptr) error {
	if len(p) == 0 {
		return nil
	}
	var pin runtime.Pinner
	pin.Pin(unsafe.SliceData(p))
	defer pin.Unpin()
	for len(p) > 0 {
		n := len(p)
		if offset < pendingByte && int64(n) > pendingByte-offset {
			n = int(pendingByte - offset)
		}
		rc := function[readWriteFunc](ptr)(f.tls, f.native, uintptr(unsafe.Pointer(unsafe.SliceData(p))), int32(n), physicalOffset(offset))
		runtime.KeepAlive(p)
		if err := nativeResult(rc); err != nil {
			return err
		}
		offset += int64(n)
		p = p[n:]
	}
	return nil
}

func (f *vfsFile) read(p []byte, offset int64) error { return f.nativeIO(p, offset, f.methods.FxRead) }
func (f *vfsFile) write(p []byte, offset int64) error {
	return f.nativeIO(p, offset, f.methods.FxWrite)
}
func (f *vfsFile) size() (int64, error) {
	var size int64
	var pin runtime.Pinner
	pin.Pin(&size)
	defer pin.Unpin()
	rc := function[pointerFunc](f.methods.FxFileSize)(f.tls, f.native, uintptr(unsafe.Pointer(&size)))
	if size >= pendingByte+lockGap {
		size -= lockGap
	} else if size > pendingByte {
		size = pendingByte
	}
	return size, nativeResult(rc)
}
func (f *vfsFile) truncate(size int64) error {
	return nativeResult(function[truncateFunc](f.methods.FxTruncate)(f.tls, f.native, physicalOffset(size)))
}
func (f *vfsFile) sync(flags int32) error {
	if flags == 0 {
		flags = sqlite.SQLITE_SYNC_FULL
	}
	return nativeResult(function[flagFunc](f.methods.FxSync)(f.tls, f.native, flags))
}

func vfsClose(tls *libc.TLS, file uintptr) int32 {
	f := state(tls, file)
	if f == nil {
		return sqlite.SQLITE_IOERR_CLOSE
	}
	if f.compressed != nil {
		f.compressed.close()
	}
	rc := function[closeFunc](f.methods.FxClose)(tls, f.native)
	libc.Xfree(tls, f.native)
	files.Delete(file)
	nativeStruct[sqlite.Tsqlite3_file](file).FpMethods = 0
	return rc
}

func vfsRead(tls *libc.TLS, file, data uintptr, count int32, offset int64) int32 {
	f := state(tls, file)
	if f == nil || count < 0 {
		return sqlite.SQLITE_IOERR_READ
	}
	if f.compressed == nil {
		return function[readWriteFunc](f.methods.FxRead)(tls, f.native, data, count, offset)
	}
	return sharedRead(f, func() int32 {
		return result(f.compressed.read(libc.GoBytes(data, int(count)), offset), sqlite.SQLITE_IOERR_READ)
	})
}
func vfsWrite(tls *libc.TLS, file, data uintptr, count int32, offset int64) int32 {
	f := state(tls, file)
	if f == nil || count < 0 {
		return sqlite.SQLITE_IOERR_WRITE
	}
	if f.compressed == nil {
		return function[readWriteFunc](f.methods.FxWrite)(tls, f.native, data, count, offset)
	}
	return result(f.compressed.write(libc.GoBytes(data, int(count)), offset), sqlite.SQLITE_IOERR_WRITE)
}
func vfsTruncate(tls *libc.TLS, file uintptr, size int64) int32 {
	f := state(tls, file)
	if f == nil {
		return sqlite.SQLITE_IOERR_TRUNCATE
	}
	if f.compressed == nil {
		return function[truncateFunc](f.methods.FxTruncate)(tls, f.native, size)
	}
	return result(f.compressed.truncate(size), sqlite.SQLITE_IOERR_TRUNCATE)
}
func vfsSync(tls *libc.TLS, file uintptr, flags int32) int32 {
	f := state(tls, file)
	if f == nil {
		return sqlite.SQLITE_IOERR_FSYNC
	}
	if f.compressed == nil {
		return function[flagFunc](f.methods.FxSync)(tls, f.native, flags)
	}
	return result(f.compressed.sync(flags), sqlite.SQLITE_IOERR_FSYNC)
}
func vfsSize(tls *libc.TLS, file, output uintptr) int32 {
	f := state(tls, file)
	if f == nil {
		return sqlite.SQLITE_IOERR_FSTAT
	}
	if f.compressed == nil {
		return function[pointerFunc](f.methods.FxFileSize)(tls, f.native, output)
	}
	return sharedRead(f, func() int32 {
		if err := f.compressed.refresh(); err != nil {
			return result(err, sqlite.SQLITE_IOERR_FSTAT)
		}
		libc.AssignPtrInt64(output, f.compressed.length)
		return sqlite.SQLITE_OK
	})
}

// .
// .
// .
func sharedRead(f *vfsFile, read func() int32) (rc int32) {
	state := f.tls.Alloc(4)
	defer f.tls.Free(4)
	if rc := function[controlFunc](f.methods.FxFileControl)(f.tls, f.native, sqlite.SQLITE_FCNTL_LOCKSTATE, state); rc != sqlite.SQLITE_OK {
		return rc
	}
	previous := *nativeStruct[int32](state)
	if previous >= sqlite.SQLITE_LOCK_SHARED {
		return read()
	}
	if rc := function[flagFunc](f.methods.FxLock)(f.tls, f.native, sqlite.SQLITE_LOCK_SHARED); rc != sqlite.SQLITE_OK {
		return rc
	}
	defer func() {
		if unlocked := function[flagFunc](f.methods.FxUnlock)(f.tls, f.native, previous); rc == sqlite.SQLITE_OK {
			rc = unlocked
		}
	}()
	return read()
}
func vfsLock(tls *libc.TLS, file uintptr, flags int32) int32 {
	f := state(tls, file)
	if f == nil {
		return sqlite.SQLITE_IOERR_LOCK
	}
	return function[flagFunc](f.methods.FxLock)(tls, f.native, flags)
}
func vfsUnlock(tls *libc.TLS, file uintptr, flags int32) int32 {
	f := state(tls, file)
	if f == nil {
		return sqlite.SQLITE_IOERR_UNLOCK
	}
	return function[flagFunc](f.methods.FxUnlock)(tls, f.native, flags)
}
func vfsReserved(tls *libc.TLS, file, output uintptr) int32 {
	f := state(tls, file)
	if f == nil {
		return sqlite.SQLITE_IOERR_CHECKRESERVEDLOCK
	}
	return function[pointerFunc](f.methods.FxCheckReservedLock)(tls, f.native, output)
}
func vfsSectorSize(tls *libc.TLS, file uintptr) int32 {
	f := state(tls, file)
	if f == nil {
		return blockSize
	}
	return function[closeFunc](f.methods.FxSectorSize)(tls, f.native)
}
func vfsDevice(tls *libc.TLS, file uintptr) int32 {
	f := state(tls, file)
	if f == nil || f.compressed != nil {
		return 0
	}
	return function[closeFunc](f.methods.FxDeviceCharacteristics)(tls, f.native)
}
func vfsControl(tls *libc.TLS, file uintptr, op int32, arg uintptr) int32 {
	f := state(tls, file)
	if f == nil {
		return sqlite.SQLITE_NOTFOUND
	}
	if op == sqlite.SQLITE_FCNTL_VFS_POINTER {
		libc.AssignPtrUintptr(arg, ownVFS)
		return sqlite.SQLITE_OK
	}
	if op == sqlite.SQLITE_FCNTL_PRAGMA && arg != 0 {
		args := nativeStruct[[3]uintptr](arg)
		if libc.GoString(args[1]) == FormatPragma && args[2] == 0 {
			format := FormatSQLite
			if f.compressed != nil {
				format = FormatZstd
			}
			return pragmaText(tls, args, format)
		}
		if libc.GoString(args[1]) == CompactPragma && args[2] == 0 {
			saved, rc := compactLocked(f)
			if rc != sqlite.SQLITE_OK {
				return rc
			}
			return pragmaText(tls, args, strconv.FormatInt(saved, 10))
		}
	}
	if f.compressed == nil {
		return function[controlFunc](f.methods.FxFileControl)(tls, f.native, op, arg)
	}
	switch op {
	case sqlite.SQLITE_FCNTL_SIZE_HINT, sqlite.SQLITE_FCNTL_CHUNK_SIZE:
		// .
		return sqlite.SQLITE_OK
	case sqlite.SQLITE_FCNTL_MMAP_SIZE:
		libc.AssignPtrInt64(arg, 0)
		return sqlite.SQLITE_OK
	case sqlite.SQLITE_FCNTL_POWERSAFE_OVERWRITE:
		libc.AssignPtrInt32(arg, 0)
		return sqlite.SQLITE_OK
	case sqlite.SQLITE_FCNTL_LOCKSTATE, sqlite.SQLITE_FCNTL_HAS_MOVED, sqlite.SQLITE_FCNTL_PERSIST_WAL:
		return function[controlFunc](f.methods.FxFileControl)(tls, f.native, op, arg)
	default:
		// .
		// .
		return sqlite.SQLITE_NOTFOUND
	}
}

func pragmaText(tls *libc.TLS, args *[3]uintptr, text string) int32 {
	output := sqlite.Xsqlite3_malloc64(tls, uint64(len(text)+1))
	if output == 0 {
		return sqlite.SQLITE_NOMEM
	}
	copy(libc.GoBytes(output, len(text)+1), text+"\x00")
	args[0] = output
	return sqlite.SQLITE_OK
}

func compactLocked(f *vfsFile) (saved int64, rc int32) {
	if f.readOnly {
		return 0, sqlite.SQLITE_READONLY
	}
	if f.compressed == nil {
		return 0, sqlite.SQLITE_OK
	}
	state := f.tls.Alloc(4)
	defer f.tls.Free(4)
	if rc := function[controlFunc](f.methods.FxFileControl)(f.tls, f.native, sqlite.SQLITE_FCNTL_LOCKSTATE, state); rc != sqlite.SQLITE_OK {
		return 0, rc
	}
	previous := *nativeStruct[int32](state)
	if previous > sqlite.SQLITE_LOCK_SHARED {
		return 0, sqlite.SQLITE_BUSY
	}
	defer func() {
		if unlocked := function[flagFunc](f.methods.FxUnlock)(f.tls, f.native, previous); rc == sqlite.SQLITE_OK {
			rc = unlocked
		}
	}()
	if rc := function[flagFunc](f.methods.FxLock)(f.tls, f.native, sqlite.SQLITE_LOCK_SHARED); rc != sqlite.SQLITE_OK {
		return 0, rc
	}
	if rc := function[flagFunc](f.methods.FxLock)(f.tls, f.native, sqlite.SQLITE_LOCK_EXCLUSIVE); rc != sqlite.SQLITE_OK {
		return 0, rc
	}
	saved, err := f.compressed.compact(sqlite.SQLITE_SYNC_FULL)
	return saved, result(err, sqlite.SQLITE_IOERR)
}
func vfsShmMap(tls *libc.TLS, file uintptr, region, size, extend int32, output uintptr) int32 {
	f := state(tls, file)
	if f == nil || f.methods.FiVersion < 2 || f.methods.FxShmMap == 0 {
		return sqlite.SQLITE_IOERR_SHMMAP
	}
	return function[shmMapFunc](f.methods.FxShmMap)(tls, f.native, region, size, extend, output)
}
func vfsShmLock(tls *libc.TLS, file uintptr, offset, n, flags int32) int32 {
	f := state(tls, file)
	if f == nil || f.methods.FiVersion < 2 || f.methods.FxShmLock == 0 {
		return sqlite.SQLITE_IOERR_SHMLOCK
	}
	return function[shmLockFunc](f.methods.FxShmLock)(tls, f.native, offset, n, flags)
}
func vfsShmBarrier(tls *libc.TLS, file uintptr) {
	f := state(tls, file)
	if f != nil && f.methods.FiVersion >= 2 && f.methods.FxShmBarrier != 0 {
		function[barrierFunc](f.methods.FxShmBarrier)(tls, f.native)
	}
}
func vfsShmUnmap(tls *libc.TLS, file uintptr, deleteFlag int32) int32 {
	f := state(tls, file)
	if f == nil || f.methods.FiVersion < 2 || f.methods.FxShmUnmap == 0 {
		return sqlite.SQLITE_OK
	}
	return function[flagFunc](f.methods.FxShmUnmap)(tls, f.native, deleteFlag)
}
func vfsFetch(tls *libc.TLS, file uintptr, offset int64, count int32, output uintptr) int32 {
	f := state(tls, file)
	libc.AssignPtrUintptr(output, 0)
	if f == nil {
		return sqlite.SQLITE_IOERR_READ
	}
	if f.compressed == nil && f.methods.FiVersion >= 3 && f.methods.FxFetch != 0 {
		return function[fetchFunc](f.methods.FxFetch)(tls, f.native, offset, count, output)
	}
	return sqlite.SQLITE_OK
}
func vfsUnfetch(tls *libc.TLS, file uintptr, offset int64, data uintptr) int32 {
	f := state(tls, file)
	if f == nil {
		return sqlite.SQLITE_IOERR_READ
	}
	if f.compressed == nil && f.methods.FiVersion >= 3 && f.methods.FxUnfetch != 0 {
		return function[unfetchFunc](f.methods.FxUnfetch)(tls, f.native, offset, data)
	}
	return sqlite.SQLITE_OK
}
