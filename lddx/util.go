package lddx

import (
	"path/filepath"
)

// ResolveAbsPath resolves a given filepath to an absolute
// path, following symlinks if necessary.
func ResolveAbsPath(path string) (string, error) {
	path, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path, err
	}

	return filepath.Abs(path)
}
