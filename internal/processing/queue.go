package processing

import (
	"context"
	"fmt"
	"io"

	repo "mediahub_oss/internal/repository"
)

func (p *Processor) queueFile(
	ctx context.Context,
	file io.ReadSeeker,
	db repo.Database,
	req EntryRequest,
	plan ProcessingPlan,
) (repo.Entry, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return repo.Entry{}, fmt.Errorf("failed to seek file: %w", err)
	}

	createdEntry, err := p.createPreliminaryEntry(ctx, db, req, plan, repo.EntryStatusProcessing, false)
	if err != nil {
		return repo.Entry{}, err
	}

	cleanupDB := func() {
		if _, delErr := p.Repo.DeleteEntry(ctx, db.ID, createdEntry.ID); delErr != nil {
			_ = p.Repo.UpdateEntriesStatus(ctx, db.ID, []int64{createdEntry.ID}, repo.EntryStatusError)
		}
	}

	fileSize, err := p.Storage.Write(ctx, db.ID.String(), createdEntry.ID, file)
	if err != nil {
		_ = p.Storage.Delete(ctx, db.ID.String(), createdEntry.ID)
		cleanupDB()
		return repo.Entry{}, fmt.Errorf("failed to write file to storage: %w", err)
	}

	createdEntry.Size = uint64(fileSize)
	createdEntry.Status = repo.EntryStatusQueued
	finalEntry, err := p.Repo.UpdateEntry(ctx, db.ID, createdEntry)
	if err != nil {
		_ = p.Storage.Delete(ctx, db.ID.String(), createdEntry.ID)
		cleanupDB()
		return repo.Entry{}, fmt.Errorf("failed to update queued entry size: %w", err)
	}

	p.Logger.Debug("Successfully queued file for processing", "database_id", db.ID.String(), "entry_id", finalEntry.ID, "filename", finalEntry.FileName)
	p.markEntryQueued()
	return finalEntry, nil
}

// findNextQueuedEntry scans for the oldest queued entry across all databases with pending queued items.
// Returns:
//   - repo.Entry: The oldest queued entry retrieved for processing, or an empty struct if none found.
//   - repo.ULID: The ULID of the database that contains the returned entry, or empty string if none found.
//   - bool (found): True if a pending queued entry was found and returned; false if queues are empty.
//   - error: Any database error encountered while querying database IDs or queued entries.
func (p *Processor) findNextQueuedEntry(ctx context.Context) (repo.Entry, repo.ULID, bool, error) {
	dbIDs, err := p.Repo.GetDatabaseULIDsWithQueuedEntries(ctx)
	if err != nil {
		return repo.Entry{}, "", false, err
	}

	if len(dbIDs) == 0 {
		p.mu.Lock()
		p.hasQueuedEntries = false
		p.mu.Unlock()
		return repo.Entry{}, "", false, nil
	}

	for _, dbID := range dbIDs {
		entries, err := p.Repo.GetEntriesByStatus(ctx, dbID, repo.EntryStatusQueued, 1)
		if err != nil {
			return repo.Entry{}, "", false, err
		}
		if len(entries) > 0 {
			p.mu.Lock()
			p.hasQueuedEntries = true
			p.mu.Unlock()
			return entries[0], dbID, true, nil
		}
	}

	// Checked all candidate databases, but no queued entries remain (e.g., claimed concurrently or removed)
	p.mu.Lock()
	p.hasQueuedEntries = false
	p.mu.Unlock()

	return repo.Entry{}, "", false, nil
}

