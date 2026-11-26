package shared

import (
	"context"
	"errors"
	"fmt"
	"mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared/customerrors"
	"mediahub_oss/internal/storage"
	"time"
)

const deletionCleanupTimeout = 15 * time.Second

func detachedCleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(ctx), deletionCleanupTimeout)
}

// DeleteSafe safely deletes a single entry from the DB and storage using a 2-Phase approach.
// Returns the entry data of the deleted file and any error if encountered.
func DeleteSafe(ctx context.Context, repo repository.Repository, storage storage.StorageProvider, dbID repository.ULID, id int64) (repository.DeletedEntryMeta, error) {

	// PHASE 1: LOCK
	// Mark as "Deleting" so it disappears from normal API usage
	if err := repo.UpdateEntriesStatus(ctx, dbID, []int64{id}, repository.EntryStatusDeleting); err != nil {
		return repository.DeletedEntryMeta{}, err // Abort early; database untouched, files untouched!
	}

	// PHASE 2: STORAGE DELETION
	// Attempt to delete the main file from storage
	err := storage.Delete(ctx, dbID, id)

	cleanupCtx, cancel := detachedCleanupContext(ctx)
	defer cancel()

	if err != nil {
		// PHASE 3: ROLLBACK
		// Storage deletion failed, revert stuck file to Error status so admins can investigate
		_ = repo.UpdateEntriesStatus(cleanupCtx, dbID, []int64{id}, repository.EntryStatusError)

		return repository.DeletedEntryMeta{}, err
	}

	// We only try to delete the preview if the main file deletion succeeded
	_ = storage.DeletePreview(cleanupCtx, dbID, id)

	// PHASE 3: COMMIT
	// Hard delete the record that was successfully wiped from disk
	deletedMeta, deleteErr := repo.DeleteEntry(cleanupCtx, dbID, id)
	if deleteErr != nil {
		return repository.DeletedEntryMeta{}, deleteErr
	}
	return deletedMeta, nil

}

// Function to delete files with database entries in a 2-phase approach, to avoid discrepancies
// between the database and the storage.
// Returns
// - entry data of deleted files
// - error if any
func DeleteMultipleSafe(ctx context.Context, repo repository.Repository, storage storage.StorageProvider, dbID repository.ULID, ids []int64) ([]repository.DeletedEntryMeta, error) {

	// PHASE 1: LOCK
	// Mark as "Deleting" so they disappear from normal API usage
	if err := repo.UpdateEntriesStatus(ctx, dbID, ids, repository.EntryStatusDeleting); err != nil {
		return make([]repository.DeletedEntryMeta, 0), err // Abort early; database untouched, files untouched!
	}

	// PHASE 2: STORAGE DELETION
	delResult, err := storage.DeleteMultiple(ctx, dbID, ids)

	cleanupCtx, cancel := detachedCleanupContext(ctx)
	defer cancel()

	// We only try to delete previews for the files where the main file deletion succeeded
	if len(delResult.Success) > 0 {
		_, _ = storage.DeleteMultiplePreviews(cleanupCtx, dbID, delResult.Success)
	}

	// PHASE 3: COMMIT OR ROLLBACK
	var deletedMeta []repository.DeletedEntryMeta
	var newerr error

	// Commit: Hard delete the records that were successfully wiped from disk
	if len(delResult.Success) > 0 {
		deletedMeta, newerr = repo.DeleteEntries(cleanupCtx, dbID, delResult.Success)
		err = errors.Join(err, newerr)
	}

	// Rollback: Revert stuck files to Error status so admins can investigate
	if len(delResult.Failed) > 0 {
		_ = repo.UpdateEntriesStatus(cleanupCtx, dbID, delResult.Failed, repository.EntryStatusError)
	}

	return deletedMeta, err
}

// DeleteDatabaseSafe safely deletes a complete database including all physical storage files and DB records.
// The database record is only removed once the storage wipe succeeded; deleting
// the record first (or despite a failed wipe) would leave orphaned files that no
// reconciliation tool could ever rediscover.
func DeleteDatabaseSafe(ctx context.Context, repo repository.Repository, storage storage.StorageProvider, dbID repository.ULID) error {
	if dbID.IsZero() {
		return fmt.Errorf("%w: database id is required", customerrors.ErrValidation)
	}
	if _, err := repo.GetDatabase(ctx, dbID); err != nil {
		return err
	}
	if storageErr := storage.DeleteDatabase(ctx, dbID); storageErr != nil {
		return fmt.Errorf("storage deletion failed, database record kept for retry: %w", storageErr)
	}
	return repo.DeleteDatabase(ctx, dbID)
}
