//go:build !windows

package storage

import "os"

func syncDirectoryFile(directory *os.File) error {
	return directory.Sync()
}
