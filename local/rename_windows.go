package local

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	filesystem "github.com/faustbrian/go-filesystem/v2"
	"golang.org/x/sys/windows"
)

// renameNoReplace uses directory-relative NT handles, opening the source entry
// itself (including reparse points) rather than following its symlink target.
// ReplaceIfExists remains false: no retry may enable replacement.
func renameNoReplace(source *os.File, sourceName string, destination *os.File, destinationName string) error {
	for _, name := range []string{sourceName, destinationName} {
		if name == "." || !filepath.IsLocal(name) || strings.Contains(name, "?") {
			return syscall.EINVAL
		}
	}
	objectName, err := windows.NewNTUnicodeString(sourceName)
	if err != nil {
		return err
	}
	attributes := windows.OBJECT_ATTRIBUTES{
		RootDirectory: windows.Handle(source.Fd()),
		ObjectName:    objectName,
	}
	attributes.Length = windowsObjectAttributesLength(windowsPointerBytes)
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&handle, windows.SYNCHRONIZE|windows.DELETE, &attributes, &status, nil, 0,
		windows.FILE_SHARE_DELETE|windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, windows.FILE_OPEN,
		windows.FILE_OPEN_REPARSE_POINT|windows.FILE_OPEN_FOR_BACKUP_INTENT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, 0)
	if err != nil {
		return renameStatusError(err)
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	filename, err := windows.UTF16FromString(destinationName)
	if err != nil {
		return err
	}
	if len(filename) > windowsRenameFilenameCapacity {
		return syscall.EINVAL
	}
	information := marshalWindowsRenameInformation(windowsPointerBytes, uint64(destination.Fd()), filename)
	err = windows.NtSetInformationFile(handle, &status, &information[0], uint32(len(information)), windows.FileRenameInformation)
	return renameStatusError(err)
}

// renameStatusError retains Win32 classification so errors.Is can distinguish
// collisions/absence while unavailable exclusive semantics remain fail-closed.
func renameStatusError(err error) error {
	if status, ok := err.(windows.NTStatus); ok {
		err = status.Errno()
	}
	if errors.Is(err, windows.ERROR_NOT_SUPPORTED) || errors.Is(err, windows.ERROR_CALL_NOT_IMPLEMENTED) {
		return errors.Join(filesystem.Unsupported("local", filesystem.CapabilityMove, filesystem.OperationMove), err)
	}
	return err
}
