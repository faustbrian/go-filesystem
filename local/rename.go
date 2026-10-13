package local

import (
	"os"
	"path"
)

// RenameNoReplace opens both parent directories through os.Root and passes only
// leaf names to the native operation. No ambient absolute path or separately
// checked path resolution is used; every acquired parent handle is closed.
func (r osRoot) RenameNoReplace(source, destination string) error {
	sourceParent, err := r.root.Open(path.Dir(source))
	if err != nil {
		return err
	}
	defer sourceParent.Close()
	destinationParent, err := r.root.Open(path.Dir(destination))
	if err != nil {
		return err
	}
	defer destinationParent.Close()
	err = renameNoReplace(sourceParent, path.Base(source), destinationParent, path.Base(destination))
	if err != nil {
		return &os.LinkError{Op: "rename-no-replace", Old: source, New: destination, Err: err}
	}
	return nil
}
