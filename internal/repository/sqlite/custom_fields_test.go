package sqlite_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/repository/sqlite"
)

func TestSQLite_BooleanCustomFieldScanning(t *testing.T) {
	ctx := context.Background()

	r, err := sqlite.NewRepository(":memory:")
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer r.Close()

	if err := r.MigrateUp(ctx); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	db, err := r.CreateDatabase(ctx, repo.Database{
		Name:        "bool_cf_db",
		ContentType: "image",
		CustomFields: []repo.CustomFieldDef{
			{Name: "is_favorite", Type: repo.CustomFieldTypeBoolean, IsIndexed: true},
			{Name: "is_archived", Type: repo.CustomFieldTypeBoolean, IsIndexed: false},
		},
	})
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}

	created, err := r.CreateEntry(ctx, db, repo.Entry{
		FileName:    "flag.jpg",
		MimeType:    "image/jpeg",
		Size:        1024,
		Status:      repo.EntryStatusReady,
		MediaFields: map[string]any{"width": 100, "height": 100},
		CustomFields: map[string]any{
			"is_favorite": true,
			"is_archived": false,
		},
	})
	if err != nil {
		t.Fatalf("failed to create entry: %v", err)
	}

	// Also insert an entry leaving boolean custom fields NULL
	nullEntry, err := r.CreateEntry(ctx, db, repo.Entry{
		FileName:    "null_flag.jpg",
		MimeType:    "image/jpeg",
		Size:        512,
		Status:      repo.EntryStatusReady,
		MediaFields: map[string]any{"width": 50, "height": 50},
	})
	if err != nil {
		t.Fatalf("failed to create null entry: %v", err)
	}

	fetched, err := r.GetEntry(ctx, db.ID, created.ID)
	if err != nil {
		t.Fatalf("GetEntry failed: %v", err)
	}

	fav, ok := fetched.CustomFields["is_favorite"].(bool)
	if !ok || !fav {
		t.Fatalf("expected is_favorite to be bool(true), got %T (%v)", fetched.CustomFields["is_favorite"], fetched.CustomFields["is_favorite"])
	}
	archived, ok := fetched.CustomFields["is_archived"].(bool)
	if !ok || archived {
		t.Fatalf("expected is_archived to be bool(false), got %T (%v)", fetched.CustomFields["is_archived"], fetched.CustomFields["is_archived"])
	}

	// Verify GetEntries and SearchEntries also return Go bools
	list, err := r.GetEntries(ctx, db.ID, repo.QueryOptions{Order: "asc"})
	if err != nil || len(list) != 2 {
		t.Fatalf("GetEntries failed: len=%d err=%v", len(list), err)
	}
	if v, ok := list[0].CustomFields["is_favorite"].(bool); !ok || !v {
		t.Fatalf("GetEntries expected bool(true), got %T (%v)", list[0].CustomFields["is_favorite"], list[0].CustomFields["is_favorite"])
	}
	if _, exists := list[1].CustomFields["is_favorite"]; exists {
		t.Fatalf("expected NULL boolean custom field to remain absent, got %v", list[1].CustomFields["is_favorite"])
	}

	// Verify GET -> PATCH round trip works cleanly
	fetched.CustomFields["is_favorite"] = false
	if _, err := r.UpdateEntryMetadata(ctx, db.ID, fetched); err != nil {
		t.Fatalf("UpdateEntryMetadata failed: %v", err)
	}
	updated, err := r.GetEntry(ctx, db.ID, created.ID)
	if err != nil {
		t.Fatalf("GetEntry after update failed: %v", err)
	}
	if v, ok := updated.CustomFields["is_favorite"].(bool); !ok || v {
		t.Fatalf("expected updated is_favorite bool(false), got %T (%v)", updated.CustomFields["is_favorite"], updated.CustomFields["is_favorite"])
	}
	_ = nullEntry
}

func TestSQLite_CustomFieldsCache_SliceAliasingImmunity(t *testing.T) {
	ctx := context.Background()

	r, err := sqlite.NewRepository(":memory:")
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer r.Close()

	if err := r.MigrateUp(ctx); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	db, err := r.CreateDatabase(ctx, repo.Database{
		Name:        "alias_test_db",
		ContentType: "image",
		CustomFields: []repo.CustomFieldDef{
			{Name: "photographer", Type: repo.CustomFieldTypeText, IsIndexed: true},
		},
	})
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}

	// Cold fill cache
	fields1, err := r.GetCustomFields(ctx, db.ID)
	if err != nil || len(fields1) != 1 {
		t.Fatalf("GetCustomFields failed: len=%d err=%v", len(fields1), err)
	}

	// Mutate returned slice and append
	fields1[0].Name = "tampered"
	_ = append(fields1, repo.CustomFieldDef{ID: 99, Name: "injected", Type: repo.CustomFieldTypeInteger})

	// Cache hit must return untouched original values
	fields2, err := r.GetCustomFields(ctx, db.ID)
	if err != nil {
		t.Fatalf("second GetCustomFields failed: %v", err)
	}
	if len(fields2) != 1 || fields2[0].Name != "photographer" {
		t.Fatalf("cache was poisoned by caller slice mutation! got: %+v", fields2)
	}
}

func TestSQLite_CustomFieldsCache_ConcurrentDDLAndReads(t *testing.T) {
	ctx := context.Background()

	r, err := sqlite.NewRepository(":memory:")
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer r.Close()

	if err := r.MigrateUp(ctx); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	db, err := r.CreateDatabase(ctx, repo.Database{
		Name:        "concurrent_cf_db",
		ContentType: "image",
	})
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}

	const workers = 4
	const fieldsCount = 12
	var wg sync.WaitGroup
	errCh := make(chan error, 64)

	// Writer goroutine performing Add -> Update -> Delete DDL while readers hammer GetCustomFields
	wg.Add(1)
	go func() {
		defer wg.Done()
		var createdIDs []int
		for i := 0; i < fieldsCount; i++ {
			cf, err := r.AddCustomField(ctx, db.ID, repo.CustomFieldDef{
				Name:      fmt.Sprintf("field_%d", i),
				Type:      repo.CustomFieldTypeText,
				IsIndexed: i%2 == 0,
			})
			if err != nil {
				errCh <- fmt.Errorf("AddCustomField %d: %w", i, err)
				return
			}
			createdIDs = append(createdIDs, cf.ID)

			newName := fmt.Sprintf("renamed_%d", i)
			if _, err := r.UpdateCustomField(ctx, db.ID, cf.ID, &newName, nil); err != nil {
				errCh <- fmt.Errorf("UpdateCustomField %d: %w", cf.ID, err)
				return
			}
		}
		for _, id := range createdIDs {
			if err := r.DeleteCustomField(ctx, db.ID, id); err != nil {
				errCh <- fmt.Errorf("DeleteCustomField %d: %w", id, err)
				return
			}
		}
	}()

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 40; i++ {
				fields, err := r.GetCustomFields(ctx, db.ID)
				if err != nil {
					errCh <- fmt.Errorf("GetCustomFields: %w", err)
					return
				}
				if len(fields) > 0 {
					fields[0].Name = "local_mutation"
				}
			}
		}()
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent custom field error: %v", err)
	}

	// After writer finishes deleting all fields, GetCustomFields must reflect empty slice (not stale cache!)
	finalFields, err := r.GetCustomFields(ctx, db.ID)
	if err != nil {
		t.Fatalf("final GetCustomFields failed: %v", err)
	}
	if len(finalFields) != 0 {
		t.Fatalf("expected 0 custom fields after concurrent DDL, cache stale: %+v", finalFields)
	}
}
