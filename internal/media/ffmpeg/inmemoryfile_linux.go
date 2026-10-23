//go:build !windows

package ffmpeg

import (
	"os"
	"syscall"
)

const (
	// maxInMemoryInputBytes caps the input size eligible for /dev/shm so
	// large on-the-fly conversions go to disk instead of exhausting RAM.
	maxInMemoryInputBytes = 32 << 20 // 32 MiB
	// minShmFreeBytes is the minimum free space required on /dev/shm.
	minShmFreeBytes = 32 << 20 // 32 MiB
)

// getTempDir returns "/dev/shm" for small inputs when /dev/shm is mounted with
// sufficient free space, falling back to the standard OS temp directory for
// large inputs or constrained /dev/shm mounts.
func getTempDir(requestedDir string, inputSize int64) string {
	if requestedDir != "" {
		return requestedDir
	}

	if inputSize < 0 || inputSize > maxInMemoryInputBytes {
		return ""
	}

	var stat syscall.Statfs_t
	if err := syscall.Statfs("/dev/shm", &stat); err == nil {
		freeBytes := uint64(stat.Bavail) * uint64(stat.Bsize)
		if freeBytes >= minShmFreeBytes && freeBytes >= uint64(inputSize)*4 {
			return "/dev/shm"
		}
	}

	return ""
}

// createInMemoryFile creates a temporary file in /dev/shm (for small files with
// sufficient RAM space) or the standard OS temp directory, and closes the handle
// so FFmpeg can write to it.
func createInMemoryFile(dir, pattern string, inputSize int64) (string, error) {
	optimalDir := getTempDir(dir, inputSize)

	tmpFile, err := os.CreateTemp(optimalDir, pattern)
	if err != nil {
		if optimalDir != "" && dir == "" {
			tmpFile, err = os.CreateTemp("", pattern)
		}
		if err != nil {
			return "", err
		}
	}

	tmpPath := tmpFile.Name()

	// Close the handle immediately so FFmpeg can take exclusive ownership
	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpPath)
		return "", err
	}

	return tmpPath, nil
}
