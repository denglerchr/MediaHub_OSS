//go:build windows

package ffmpeg

import (
	"os"
	"syscall"
)

const (
	// maxInMemoryInputBytes caps the input size eligible for the Windows RAM cache
	// (FILE_ATTRIBUTE_TEMPORARY) so large conversions go to disk.
	maxInMemoryInputBytes = 32 << 20 // 32 MiB
)

// createInMemoryFile creates a temp file and flags it as non-indexed (to block
// the Windows Search Indexer) and, for small inputs, as temporary (to keep it in RAM cache).
func createInMemoryFile(dir, pattern string, inputSize int64) (string, error) {
	// 1. Create the file using the standard Go library
	tmpFile, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", err
	}

	tmpPath := tmpFile.Name()

	// 2. Close the handle immediately so FFmpeg can access it exclusively
	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpPath)
		return "", err
	}

	// 3. Convert the Go string to a UTF-16 pointer (required by Windows APIs)
	pathPtr, err := syscall.UTF16PtrFromString(tmpPath)
	if err == nil {
		// 4. Fetch the current attributes (like standard Archive flags)
		currentAttributes, err := syscall.GetFileAttributes(pathPtr)
		if err == nil {
			// 5. Apply our optimizations using bitwise OR:
			// 0x100  = FILE_ATTRIBUTE_TEMPORARY (Force to RAM cache for small inputs)
			// 0x2000 = FILE_ATTRIBUTE_NOT_CONTENT_INDEXED (Block Search Indexer locks)
			optimizedAttributes := currentAttributes | 0x2000
			if inputSize >= 0 && inputSize <= maxInMemoryInputBytes {
				optimizedAttributes |= 0x100
			}

			syscall.SetFileAttributes(pathPtr, optimizedAttributes)
		}
	}

	return tmpPath, nil
}
