package localstorage

import (
	"fmt"
	"io"
	"io/fs"
	"mediahub_oss/internal/shared/customerrors"
	"mediahub_oss/internal/shared/ulid"
	"mediahub_oss/internal/storage"
	"os"
	"path/filepath"
	"strconv"
)

// getFilePath generates a highly optimized, immutable path based purely on the ID.
// Files are sharded into buckets of 1000 files each to ensure maximum filesystem index performance.
func getFilePath(rootPath string, dbID ulid.ULID, id int64) string {
	// e.g., ID 10232 -> bucket "10"
	bucketDir := fmt.Sprintf("%d", id/1000)
	fileName := fmt.Sprintf("%d", id)

	return filepath.Join(rootPath, dbID.String(), bucketDir, fileName)
}

// removeFile safely deletes a file, ignoring "file not found" errors for idempotency.
func removeFile(path string) error {
	err := os.Remove(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // File already gone, treat as success
		}
		return err
	}
	return nil
}

// writeFileStream is a helper function to handle directory creation, file creation, and data streaming safely.
func writeFileStream(fullPath string, stream io.Reader) (int64, error) {
	// Ensure the parent directory bucket exists
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return 0, fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	// Write to a temporary file in the same directory to ensure atomic replacement
	tempFile, err := os.CreateTemp(dir, ".upload-tmp-*")
	if err != nil {
		return 0, fmt.Errorf("failed to create temp file in %s: %w", dir, err)
	}
	tempPath := tempFile.Name()

	// Stream the data from the reader directly to the temp file
	written, copyErr := io.Copy(tempFile, stream)
	closeErr := tempFile.Close()

	if copyErr != nil {
		os.Remove(tempPath)
		return written, fmt.Errorf("failed to stream data to file: %w", copyErr)
	}
	if closeErr != nil {
		os.Remove(tempPath)
		return written, fmt.Errorf("failed to close temp file %s: %w", tempPath, closeErr)
	}

	// Atomically rename temp file to final destination
	if err := os.Rename(tempPath, fullPath); err != nil {
		os.Remove(tempPath)
		return written, fmt.Errorf("failed to move temp file to destination %s: %w", fullPath, err)
	}

	return written, nil
}

func getFileStats(filepath string) (storage.FileInfo, error) {
	info, err := os.Stat(filepath)
	if err != nil {
		if os.IsNotExist(err) {
			return storage.FileInfo{}, customerrors.ErrNotFound
		}
		return storage.FileInfo{}, err
	}

	return storage.FileInfo{
		Size:         info.Size(),
		LastModified: info.ModTime(),
	}, nil
}

// attempts deleting multiple files in a loop
// returns:
// - slice of ids that were deleted
// - slice of ids where the delete failed
// - slice of errors indicating why the delete failed
func deleteMultiple(rootPath string, dbID ulid.ULID, ids []int64) ([]int64, []int64, []error) {
	var deletedIDs []int64
	var failedIDs []int64
	var errs []error

	for _, id := range ids {
		fullPath := getFilePath(rootPath, dbID, id)
		if err := removeFile(fullPath); err != nil {
			failedIDs = append(failedIDs, id)
			errs = append(errs, err)
		} else {
			deletedIDs = append(deletedIDs, id)
		}
	}

	return deletedIDs, failedIDs, errs

}

// walkDirectory is a private helper that traverses the bucket structure and triggers the callback.
func (ds *LocalStorage) walkDirectory(basePath string, walkFn func(id int64, info storage.FileInfo) error) error {
	err := filepath.WalkDir(basePath, func(path string, d fs.DirEntry, err error) error {
		// If the directory doesn't exist at all, just return nil (nothing to walk)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}

		// Skip directories (like the bucket folders "0", "1", etc.)
		if d.IsDir() {
			return nil
		}

		// The filename is the ID
		id, err := strconv.ParseInt(d.Name(), 10, 64)
		if err != nil {
			// If there's a file that isn't a number (e.g., .DS_Store), ignore it
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		fileInfo := storage.FileInfo{
			Size:         info.Size(),
			LastModified: info.ModTime(),
		}

		// Execute the callback
		return walkFn(id, fileInfo)
	})

	return err
}
