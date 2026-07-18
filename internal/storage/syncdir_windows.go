//go:build windows

package storage

import "os"

func syncDirectoryFile(*os.File) error {
	// Go does not open Windows directory handles with the flags required by
	// FlushFileBuffers. File contents are still flushed before publication.
	return nil
}
