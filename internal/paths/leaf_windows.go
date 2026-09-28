//go:build windows

package paths

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// fileAttributeTagInfo is FILE_ATTRIBUTE_TAG_INFO, which golang.org/x/sys/windows does not declare.
// It is a Go struct rather than a byte buffer because the kernel requires the ULONG alignment a
// uint32 field guarantees and a byte array does not.
type fileAttributeTagInfo struct {
	FileAttributes uint32
	ReparseTag     uint32
}

// reparseTagNameSurrogate is the bit of a reparse tag that marks it a name surrogate: a reparse
// point that stands for another named entity (IO_REPARSE_TAG_SYMLINK, IO_REPARSE_TAG_MOUNT_POINT),
// as opposed to one that stands for the file's own content. It is IsReparseTagNameSurrogate's test,
// and the one Go's own os.Lstat uses to decide what is a symlink.
const reparseTagNameSurrogate = 0x20000000

// openSharedLeaf is OpenSharedLeaf on Windows.
//
// The first open is openShared's CreateFile plus FILE_FLAG_OPEN_REPARSE_POINT, which opens a reparse
// point itself rather than what it points to. For an ordinary file that flag changes nothing, and
// this handle is the answer: one open, so no replace can land between a check and a use.
//
// A handle on a reparse point is then judged by its tag. A name surrogate is a link, and is refused
// with ErrNotLeaf. Any other tag stands for the file's own content, and reading through a handle
// that did not follow it would skip the filter that recalls that content, so the file is opened
// again the ordinary way and that handle is kept only when it is the same file as the first; a
// replace landing between the two opens is reported as ErrLeafReplaced.
func openSharedLeaf(p string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: p, Err: err}
	}
	h, err := windows.CreateFile(name, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: p, Err: err}
	}
	var ti fileAttributeTagInfo
	if err := windows.GetFileInformationByHandleEx(h, windows.FileAttributeTagInfo,
		(*byte)(unsafe.Pointer(&ti)), uint32(unsafe.Sizeof(ti))); err != nil {
		_ = windows.CloseHandle(h)
		return nil, &os.PathError{Op: "open", Path: p, Err: err}
	}
	if ti.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0 {
		return os.NewFile(uintptr(h), p), nil
	}
	if ti.ReparseTag&reparseTagNameSurrogate != 0 {
		_ = windows.CloseHandle(h)
		return nil, &os.PathError{Op: "open", Path: p, Err: ErrNotLeaf}
	}
	defer func() { _ = windows.CloseHandle(h) }()
	f, err := openShared(p)
	if err != nil {
		return nil, err
	}
	var first, second windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(h, &first) != nil ||
		windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &second) != nil ||
		first.VolumeSerialNumber != second.VolumeSerialNumber ||
		first.FileIndexHigh != second.FileIndexHigh || first.FileIndexLow != second.FileIndexLow {
		_ = f.Close()
		return nil, &os.PathError{Op: "open", Path: p, Err: ErrLeafReplaced}
	}
	return f, nil
}
