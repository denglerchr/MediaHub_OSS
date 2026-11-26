package localstorage

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"mediahub_oss/internal/shared/customerrors"
	"mediahub_oss/internal/shared/ulid"
	"mediahub_oss/internal/storage"
	"os"
	"path/filepath"
)

type LocalStorage struct {
	RootPath string
}

// Helper struct to combine LimitReader with the underlying File's Closer
type limitedReadCloser struct {
	io.Reader
	io.Closer
}

// Write streams the file content to the local filesystem and returns the amount of bytes written.
func (ds *LocalStorage) Write(ctx context.Context, dbID ulid.ULID, id int64, content io.Reader) (int64, error) {
	// Generate the file path (e.g. rootPath/dbID/bucket/ID)
	fullPath := getFilePath(ds.RootPath, dbID, id)
	return writeFileStream(fullPath, content)
}

// WritePreview streams the preview file to the local filesystem's preview directory.
func (ds *LocalStorage) WritePreview(ctx context.Context, dbID ulid.ULID, id int64, preview io.Reader) (int64, error) {
	// Previews are stored in a separate root folder (e.g., .../storage_root/previews/)
	previewRoot := filepath.Join(ds.RootPath, "previews")
	fullPath := getFilePath(previewRoot, dbID, id)

	return writeFileStream(fullPath, preview)
}

// Stat retrieves metadata about the main file without reading the content.
func (ds *LocalStorage) Stat(ctx context.Context, dbID ulid.ULID, id int64) (storage.FileInfo, error) {
	fullPath := getFilePath(ds.RootPath, dbID, id)
	return getFileStats(fullPath)
}

// StatPreview retrieves metadata about the preview file without reading the content.
func (ds *LocalStorage) StatPreview(ctx context.Context, dbID ulid.ULID, id int64) (storage.FileInfo, error) {
	previewRoot := filepath.Join(ds.RootPath, "previews")
	fullPath := getFilePath(previewRoot, dbID, id)
	return getFileStats(fullPath)
}

// Read retrieves a stream of the file content, supporting byte-range requests.
func (ds *LocalStorage) Read(ctx context.Context, dbID ulid.ULID, id int64, offset int64, length int64) (io.ReadCloser, error) {
	fullPath := getFilePath(ds.RootPath, dbID, id)

	f, err := os.Open(fullPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, customerrors.ErrNotFound
		}
		return nil, err
	}

	// 1. Seek to the start offset
	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			f.Close()
			return nil, err
		}
	}

	// 2. If length is specified (>= 0), limit the reader
	// We must wrap it to ensure we don't lose the Close() method of the file
	if length >= 0 {
		return &limitedReadCloser{
			Reader: io.LimitReader(f, length),
			Closer: f,
		}, nil
	}

	// 3. Otherwise read until EOF
	return f, nil
}

// ReadPreview retrieves a stream of the preview file content.
func (ds *LocalStorage) ReadPreview(ctx context.Context, dbID ulid.ULID, id int64) (io.ReadCloser, error) {
	previewRoot := filepath.Join(ds.RootPath, "previews")
	fullPath := getFilePath(previewRoot, dbID, id)

	f, err := os.Open(fullPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, customerrors.ErrNotFound
		}
		return nil, err
	}

	return f, nil
}

// Delete removes the main file from storage.
func (ds *LocalStorage) Delete(ctx context.Context, dbID ulid.ULID, id int64) error {
	fullPath := getFilePath(ds.RootPath, dbID, id)
	return removeFile(fullPath)
}

// DeleteMultiple removes multiple main files from storage.
func (ds *LocalStorage) DeleteMultiple(ctx context.Context, dbID ulid.ULID, ids []int64) (storage.BulkDeleteResult, error) {
	deletedIDs, failedIDs, errs := deleteMultiple(ds.RootPath, dbID, ids)

	result := storage.BulkDeleteResult{
		Success: deletedIDs,
		Failed:  failedIDs,
	}
	return result, errors.Join(errs...)
}

// DeletePreview removes the generated preview file from storage.
func (ds *LocalStorage) DeletePreview(ctx context.Context, dbID ulid.ULID, id int64) error {
	previewRoot := filepath.Join(ds.RootPath, "previews")
	fullPath := getFilePath(previewRoot, dbID, id)

	return removeFile(fullPath)
}

// DeleteMultiplePreviews removes multiple preview files from storage.
func (ds *LocalStorage) DeleteMultiplePreviews(ctx context.Context, dbID ulid.ULID, ids []int64) (storage.BulkDeleteResult, error) {
	previewRoot := filepath.Join(ds.RootPath, "previews")

	deletedIDs, failedIDs, errs := deleteMultiple(previewRoot, dbID, ids)

	result := storage.BulkDeleteResult{
		Success: deletedIDs,
		Failed:  failedIDs,
	}
	return result, errors.Join(errs...)
}

// Walk iterates over all main files in the storage for a given database.
func (ds *LocalStorage) Walk(ctx context.Context, dbID ulid.ULID, walkFn func(id int64, info storage.FileInfo) error) error {
	basePath := filepath.Join(ds.RootPath, dbID.String())
	return ds.walkDirectory(basePath, walkFn)
}

// WalkPreview iterates over all preview files in the storage for a given database.
func (ds *LocalStorage) WalkPreview(ctx context.Context, dbID ulid.ULID, walkFn func(id int64, info storage.FileInfo) error) error {
	previewRoot := filepath.Join(ds.RootPath, "previews")
	basePath := filepath.Join(previewRoot, dbID.String())
	return ds.walkDirectory(basePath, walkFn)
}

// DeleteDatabase removes all storage objects and preview files associated with a database.
func (ds *LocalStorage) DeleteDatabase(ctx context.Context, dbID ulid.ULID) error {
	mainDir := filepath.Join(ds.RootPath, dbID.String())
	previewDir := filepath.Join(ds.RootPath, "previews", dbID.String())

	var errs []error
	if err := os.RemoveAll(mainDir); err != nil && !os.IsNotExist(err) {
		errs = append(errs, err)
	}
	if err := os.RemoveAll(previewDir); err != nil && !os.IsNotExist(err) {
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}
