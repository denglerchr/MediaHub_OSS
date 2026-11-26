package shared

import (
	"context"
	"errors"
	"io"
	"testing"

	"mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared/customerrors"
	"mediahub_oss/internal/shared/ulid"
	"mediahub_oss/internal/storage"
)

type spyDeletionRepo struct {
	repository.Repository
	getDatabaseErr    error
	getDatabaseCalled bool
	deleteCalled      bool
}

func (r *spyDeletionRepo) GetDatabase(ctx context.Context, dbID repository.ULID) (repository.Database, error) {
	r.getDatabaseCalled = true
	if r.getDatabaseErr != nil {
		return repository.Database{}, r.getDatabaseErr
	}
	return repository.Database{ID: dbID, Name: "test"}, nil
}

func (r *spyDeletionRepo) DeleteDatabase(ctx context.Context, dbID repository.ULID) error {
	r.deleteCalled = true
	return nil
}

type spyDeletionStorage struct {
	storage.StorageProvider
	deleteDatabaseCalled bool
}

func (s *spyDeletionStorage) DeleteDatabase(ctx context.Context, dbID ulid.ULID) error {
	s.deleteDatabaseCalled = true
	return nil
}

func (s *spyDeletionStorage) Read(ctx context.Context, dbID ulid.ULID, id int64, offset int64, length int64) (io.ReadCloser, error) {
	return nil, customerrors.ErrNotFound
}

func TestDeleteDatabaseSafe(t *testing.T) {
	ctx := context.Background()

	t.Run("zero ULID returns ErrValidation without touching repo or storage", func(t *testing.T) {
		repo := &spyDeletionRepo{}
		store := &spyDeletionStorage{}

		err := DeleteDatabaseSafe(ctx, repo, store, repository.ULID{})
		if !errors.Is(err, customerrors.ErrValidation) {
			t.Fatalf("expected ErrValidation for zero ULID, got %v", err)
		}
		if repo.getDatabaseCalled || repo.deleteCalled {
			t.Fatalf("expected repo not to be called for zero ULID")
		}
		if store.deleteDatabaseCalled {
			t.Fatalf("expected storage.DeleteDatabase not to be called for zero ULID")
		}
	})

	t.Run("nonexistent valid ULID returns ErrNotFound without calling storage", func(t *testing.T) {
		validMissing := ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR")
		repo := &spyDeletionRepo{getDatabaseErr: customerrors.ErrNotFound}
		store := &spyDeletionStorage{}

		err := DeleteDatabaseSafe(ctx, repo, store, validMissing)
		if !errors.Is(err, customerrors.ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
		if !repo.getDatabaseCalled {
			t.Fatalf("expected repo.GetDatabase to be called")
		}
		if store.deleteDatabaseCalled {
			t.Fatalf("expected storage.DeleteDatabase not to be called when database does not exist")
		}
		if repo.deleteCalled {
			t.Fatalf("expected repo.DeleteDatabase not to be called when database does not exist")
		}
	})

	t.Run("existing valid ULID deletes storage then repo", func(t *testing.T) {
		validID := ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR")
		repo := &spyDeletionRepo{}
		store := &spyDeletionStorage{}

		err := DeleteDatabaseSafe(ctx, repo, store, validID)
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
		if !repo.getDatabaseCalled || !store.deleteDatabaseCalled || !repo.deleteCalled {
			t.Fatalf("expected GetDatabase, storage.DeleteDatabase, and repo.DeleteDatabase to all be called")
		}
	})
}

type phase3RepoSpy struct {
	repository.Repository
	statusCalls     []repository.EntryStatus
	statusIDs       [][]int64
	deletedIDs      []int64
	deleteEntryDone bool
}

func (r *phase3RepoSpy) UpdateEntriesStatus(ctx context.Context, dbID repository.ULID, entryIDs []int64, status repository.EntryStatus) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.statusCalls = append(r.statusCalls, status)
	cp := append([]int64(nil), entryIDs...)
	r.statusIDs = append(r.statusIDs, cp)
	return nil
}

func (r *phase3RepoSpy) DeleteEntry(ctx context.Context, dbID repository.ULID, id int64) (repository.DeletedEntryMeta, error) {
	if err := ctx.Err(); err != nil {
		return repository.DeletedEntryMeta{}, err
	}
	r.deleteEntryDone = true
	r.deletedIDs = append(r.deletedIDs, id)
	return repository.DeletedEntryMeta{ID: id}, nil
}

func (r *phase3RepoSpy) DeleteEntries(ctx context.Context, dbID repository.ULID, entryIDs []int64) ([]repository.DeletedEntryMeta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.deletedIDs = append(r.deletedIDs, entryIDs...)
	metas := make([]repository.DeletedEntryMeta, 0, len(entryIDs))
	for _, id := range entryIDs {
		metas = append(metas, repository.DeletedEntryMeta{ID: id})
	}
	return metas, nil
}

type phase2CancelStorageSpy struct {
	storage.StorageProvider
	cancel              context.CancelFunc
	deleteErr           error
	bulkResult          storage.BulkDeleteResult
	bulkErr             error
	deletePreviewCalled bool
	deletePrevIDs       []int64
}

func (s *phase2CancelStorageSpy) Delete(ctx context.Context, dbID ulid.ULID, id int64) error {
	s.cancel()
	return s.deleteErr
}

func (s *phase2CancelStorageSpy) DeletePreview(ctx context.Context, dbID ulid.ULID, id int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.deletePreviewCalled = true
	return nil
}

func (s *phase2CancelStorageSpy) DeleteMultiple(ctx context.Context, dbID ulid.ULID, ids []int64) (storage.BulkDeleteResult, error) {
	s.cancel()
	return s.bulkResult, s.bulkErr
}

func (s *phase2CancelStorageSpy) DeleteMultiplePreviews(ctx context.Context, dbID ulid.ULID, ids []int64) (storage.BulkDeleteResult, error) {
	if err := ctx.Err(); err != nil {
		return storage.BulkDeleteResult{}, err
	}
	s.deletePrevIDs = append(s.deletePrevIDs, ids...)
	return storage.BulkDeleteResult{Success: ids}, nil
}

func TestDeleteSafe_CanceledDuringPhase2(t *testing.T) {
	dbID := ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR")

	t.Run("commits DB deletion and deletes preview when storage delete succeeds", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		repo := &phase3RepoSpy{}
		store := &phase2CancelStorageSpy{cancel: cancel}

		meta, err := DeleteSafe(ctx, repo, store, dbID, 42)
		if err != nil {
			t.Fatalf("expected DeleteSafe to succeed despite Phase 2 cancel, got %v", err)
		}
		if meta.ID != 42 || !repo.deleteEntryDone {
			t.Fatalf("expected entry 42 to be committed as deleted, meta=%+v deleteEntryDone=%v", meta, repo.deleteEntryDone)
		}
		if !store.deletePreviewCalled {
			t.Fatal("expected DeletePreview to succeed with detached cleanup context")
		}
		if len(repo.statusCalls) != 1 || repo.statusCalls[0] != repository.EntryStatusDeleting {
			t.Fatalf("expected only Phase 1 Deleting lock status, got %v", repo.statusCalls)
		}
	})

	t.Run("rolls back status to EntryStatusError when storage delete fails", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		storageErr := errors.New("storage failure")
		repo := &phase3RepoSpy{}
		store := &phase2CancelStorageSpy{cancel: cancel, deleteErr: storageErr}

		_, err := DeleteSafe(ctx, repo, store, dbID, 42)
		if !errors.Is(err, storageErr) {
			t.Fatalf("expected storage error, got %v", err)
		}
		if repo.deleteEntryDone {
			t.Fatal("did not expect DeleteEntry when storage delete failed")
		}
		if len(repo.statusCalls) != 2 || repo.statusCalls[1] != repository.EntryStatusError {
			t.Fatalf("expected Phase 3 rollback to EntryStatusError, got %v", repo.statusCalls)
		}
	})
}

func TestDeleteMultipleSafe_CanceledDuringPhase2(t *testing.T) {
	dbID := ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	partialErr := errors.New("partial storage error")
	repo := &phase3RepoSpy{}
	store := &phase2CancelStorageSpy{
		cancel:     cancel,
		bulkResult: storage.BulkDeleteResult{Success: []int64{1}, Failed: []int64{2}},
		bulkErr:    partialErr,
	}

	metas, err := DeleteMultipleSafe(ctx, repo, store, dbID, []int64{1, 2})
	if !errors.Is(err, partialErr) {
		t.Fatalf("expected partial storage error, got %v", err)
	}
	if len(metas) != 1 || metas[0].ID != 1 {
		t.Fatalf("expected entry 1 committed as deleted, got %+v", metas)
	}
	if len(store.deletePrevIDs) != 1 || store.deletePrevIDs[0] != 1 {
		t.Fatalf("expected preview deletion for [1], got %v", store.deletePrevIDs)
	}
	if len(repo.deletedIDs) != 1 || repo.deletedIDs[0] != 1 {
		t.Fatalf("expected repo.DeleteEntries([1]), got %v", repo.deletedIDs)
	}
	if len(repo.statusCalls) != 2 || repo.statusCalls[1] != repository.EntryStatusError {
		t.Fatalf("expected rollback status EntryStatusError, got %v", repo.statusCalls)
	}
	if len(repo.statusIDs[1]) != 1 || repo.statusIDs[1][0] != 2 {
		t.Fatalf("expected rollback for entry [2], got %v", repo.statusIDs[1])
	}
}
