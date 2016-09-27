package lddx

import (
	"path/filepath"
)

func ResolveAbsPath(path string) (string, error) {
	path, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path, err
	}

	return filepath.Abs(path)
}
