//go:build !windows

package clients

import (
	"os"
	"path/filepath"
)

func replaceCodexFile(tempPath, targetPath string) error {
	if err := os.Rename(tempPath, targetPath); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(targetPath))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
