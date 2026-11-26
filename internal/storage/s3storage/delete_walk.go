package s3storage

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strconv"

	"github.com/minio/minio-go/v7"

	"mediahub_oss/internal/shared/ulid"
	"mediahub_oss/internal/storage"
)

// Delete removes the main file from storage.
func (s *S3StorageProvider) Delete(ctx context.Context, dbID ulid.ULID, id int64) error {
	objectKey := getObjectKey(dbID, id)
	return s.client.RemoveObject(ctx, s.bucket, objectKey, minio.RemoveObjectOptions{})
}

// DeleteMultiple removes multiple main files from storage.
func (s *S3StorageProvider) DeleteMultiple(ctx context.Context, dbID ulid.ULID, ids []int64) (storage.BulkDeleteResult, error) {
	return s.deleteMultipleKeys(ctx, ids, func(id int64) string {
		return getObjectKey(dbID, id)
	})
}

// DeletePreview removes the generated preview file from storage.
func (s *S3StorageProvider) DeletePreview(ctx context.Context, dbID ulid.ULID, id int64) error {
	objectKey := getPreviewObjectKey(dbID, id)
	return s.client.RemoveObject(ctx, s.bucket, objectKey, minio.RemoveObjectOptions{})
}

// DeleteMultiplePreviews removes multiple preview files from storage.
func (s *S3StorageProvider) DeleteMultiplePreviews(ctx context.Context, dbID ulid.ULID, ids []int64) (storage.BulkDeleteResult, error) {
	return s.deleteMultipleKeys(ctx, ids, func(id int64) string {
		return getPreviewObjectKey(dbID, id)
	})
}

func (s *S3StorageProvider) deleteMultipleKeys(ctx context.Context, ids []int64, keyGen func(int64) string) (storage.BulkDeleteResult, error) {
	if len(ids) == 0 {
		return storage.BulkDeleteResult{}, nil
	}

	objectsCh := make(chan minio.ObjectInfo, len(ids))
	for _, id := range ids {
		objectsCh <- minio.ObjectInfo{Key: keyGen(id)}
	}
	close(objectsCh)

	failedMap := make(map[int64]error)
	var errs []error

	for errObj := range s.client.RemoveObjects(ctx, s.bucket, objectsCh, minio.RemoveObjectsOptions{}) {
		baseName := path.Base(errObj.ObjectName)
		if parsedID, err := strconv.ParseInt(baseName, 10, 64); err == nil {
			failedMap[parsedID] = errObj.Err
		}
		errs = append(errs, errObj.Err)
	}

	var successIDs []int64
	var failedIDs []int64

	if len(errs) > len(failedMap) {
		// There are unmapped or general errors from MinIO: treat all IDs as failed
		// to prevent deleting database records while files remain on S3.
		failedIDs = ids
	} else {
		for _, id := range ids {
			if _, failed := failedMap[id]; failed {
				failedIDs = append(failedIDs, id)
			} else {
				successIDs = append(successIDs, id)
			}
		}
	}

	return storage.BulkDeleteResult{
		Success: successIDs,
		Failed:  failedIDs,
	}, errors.Join(errs...)
}

// Walk iterates over all main files in the storage for a given database.
func (s *S3StorageProvider) Walk(ctx context.Context, dbID ulid.ULID, walkFn func(id int64, info storage.FileInfo) error) error {
	prefix := fmt.Sprintf("%s/", dbID.String())
	return s.walkObjects(ctx, prefix, walkFn)
}

// WalkPreview iterates over all preview files in the storage for a given database.
func (s *S3StorageProvider) WalkPreview(ctx context.Context, dbID ulid.ULID, walkFn func(id int64, info storage.FileInfo) error) error {
	prefix := fmt.Sprintf("previews/%s/", dbID.String())
	return s.walkObjects(ctx, prefix, walkFn)
}

func (s *S3StorageProvider) walkObjects(ctx context.Context, prefix string, walkFn func(id int64, info storage.FileInfo) error) error {
	listCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	opts := minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	}

	for object := range s.client.ListObjects(listCtx, s.bucket, opts) {
		if object.Err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return object.Err
		}

		baseName := path.Base(object.Key)
		id, err := strconv.ParseInt(baseName, 10, 64)
		if err != nil {
			continue // Skip non-numeric keys if any
		}

		fileInfo := storage.FileInfo{
			Size:         object.Size,
			LastModified: object.LastModified,
		}

		if err := walkFn(id, fileInfo); err != nil {
			return err
		}
	}

	if ctx.Err() != nil {
		return ctx.Err()
	}

	return nil
}

// DeleteDatabase removes all storage objects and preview files associated with a database from S3.
func (s *S3StorageProvider) DeleteDatabase(ctx context.Context, dbID ulid.ULID) error {
	prefixes := []string{
		fmt.Sprintf("%s/", dbID.String()),
		fmt.Sprintf("previews/%s/", dbID.String()),
	}

	var errs []error
	for _, prefix := range prefixes {
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			break
		}
		if err := s.deletePrefixObjects(ctx, prefix); err != nil {
			errs = append(errs, err)
		}
	}

	if ctxErr := ctx.Err(); ctxErr != nil && len(errs) == 0 {
		errs = append(errs, ctxErr)
	}

	return errors.Join(errs...)
}

func (s *S3StorageProvider) deletePrefixObjects(ctx context.Context, prefix string) error {
	listCtx, cancelList := context.WithCancel(ctx)
	defer cancelList()

	objectsCh := make(chan minio.ObjectInfo, 250)
	listErrCh := make(chan error, 1)
	listDone := make(chan struct{})

	go func() {
		defer close(listDone)
		defer close(objectsCh)
		opts := minio.ListObjectsOptions{
			Prefix:    prefix,
			Recursive: true,
		}
		for object := range s.client.ListObjects(listCtx, s.bucket, opts) {
			if object.Err != nil {
				listErrCh <- object.Err
				return
			}
			select {
			case <-listCtx.Done():
				if ctxErr := ctx.Err(); ctxErr != nil {
					listErrCh <- ctxErr
				}
				return
			case objectsCh <- object:
			}
		}
	}()

	var errs []error
	for errObj := range s.client.RemoveObjects(ctx, s.bucket, objectsCh, minio.RemoveObjectsOptions{}) {
		errs = append(errs, errObj.Err)
	}

	cancelList()
	<-listDone

	select {
	case listErr := <-listErrCh:
		errs = append(errs, listErr)
	default:
	}

	if ctxErr := ctx.Err(); ctxErr != nil {
		hasCtxErr := false
		for _, e := range errs {
			if errors.Is(e, ctxErr) {
				hasCtxErr = true
				break
			}
		}
		if !hasCtxErr {
			errs = append(errs, ctxErr)
		}
	}

	return errors.Join(errs...)
}
