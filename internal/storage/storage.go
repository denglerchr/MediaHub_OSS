package storage

import (
	"context"
	"io"
)

type StorageProvider interface {
	// Write uploads a file stream to the storage backend and returns the amount of bytes written.
	Write(ctx context.Context, dbID string, id int64, content io.Reader) (int64, error)

	// WritePreview uploads a preview file stream to the storage backend and returns the amount of bytes written.
	WritePreview(ctx context.Context, dbID string, id int64, preview io.Reader) (int64, error)

	// Stat retrieves metadata about the main file without downloading the content.
	Stat(ctx context.Context, dbID string, id int64) (FileInfo, error)

	// StatPreview retrieves metadata about the preview file without downloading the content.
	StatPreview(ctx context.Context, dbID string, id int64) (FileInfo, error)

	// Read retrieves a stream of the file content. Pass length<0 to get a reader for the full file.
	Read(ctx context.Context, dbID string, id int64, offset int64, length int64) (io.ReadCloser, error)

	// ReadPreview retrieves a stream of the preview file content.
	ReadPreview(ctx context.Context, dbID string, id int64) (io.ReadCloser, error)

	// Delete removes the main file from storage.
	Delete(ctx context.Context, dbID string, id int64) error

	// Delete multiple files, possibly more efficient than looping over Delete, return the ids of actually deleted files
	DeleteMultiple(ctx context.Context, dbID string, ids []int64) (BulkDeleteResult, error)

	// DeletePreview removes the generated preview file from storage.
	DeletePreview(ctx context.Context, dbID string, id int64) error

	// Delete multiple preview files, possibly more efficient than looping over DeletePreview, , return the ids of actually deleted files
	DeleteMultiplePreviews(ctx context.Context, dbID string, ids []int64) (BulkDeleteResult, error)

	// Walk iterates over all main files in the storage for a given database. It calls the provided walkFn for each discovered file.
	Walk(ctx context.Context, dbID string, walkFn func(id int64, info FileInfo) error) error

	// WalkPreview iterates over all preview files in the storage for a given database. It calls the provided walkFn for each discovered preview file.
	WalkPreview(ctx context.Context, dbID string, walkFn func(id int64, info FileInfo) error) error

	// DeleteDatabase removes all storage objects and preview files associated with a database.
	DeleteDatabase(ctx context.Context, dbID string) error
}
