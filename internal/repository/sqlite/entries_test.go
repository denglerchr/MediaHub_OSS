package sqlite_test

import (
	"context"
	"errors"
	"testing"
	"time"

	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/repository/migrations"
	"mediahub_oss/internal/repository/sqlite"
	"mediahub_oss/internal/shared"
	"mediahub_oss/internal/shared/customerrors"
	"mediahub_oss/internal/shared/ulid"
)

func TestCreateEntry_ZeroTimestamp(t *testing.T) {
	ctx := context.Background()

	// 1. Initialize SQLite repository in memory
	r, err := sqlite.NewRepository(":memory:")
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer r.Close()

	// 2. Run migrations
	if err := r.MigrateUp(context.Background()); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	// 3. Create a test database
	dbModel := repo.Database{
		Name:        "test_images",
		ContentType: "image",
	}
	createdDB, err := r.CreateDatabase(ctx, dbModel)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}

	before := time.Now().Add(-1 * time.Second)

	// 4. Create an entry with zero Timestamp
	entry := repo.Entry{
		FileName: "test.jpg",
		MimeType: "image/jpeg",
		Size:     1024,
		Status:   repo.EntryStatusReady,
		MediaFields: map[string]any{
			"width":  800,
			"height": 600,
		},
		// Timestamp is left zero time.Time{}
	}

	createdEntry, err := r.CreateEntry(ctx, createdDB, entry)
	if err != nil {
		t.Fatalf("failed to create entry: %v", err)
	}

	after := time.Now().Add(1 * time.Second)

	// Check returned entry timestamp
	if createdEntry.Timestamp.IsZero() {
		t.Errorf("expected createdEntry.Timestamp to be populated, got zero time")
	}
	if createdEntry.Timestamp.Before(before) || createdEntry.Timestamp.After(after) {
		t.Errorf("expected createdEntry.Timestamp to be around now, got %v (unix milli: %d)", createdEntry.Timestamp, createdEntry.Timestamp.UnixMilli())
	}
	if createdEntry.Timestamp.UnixMilli() < 0 {
		t.Errorf("timestamp was written as negative epoch (year 0001): %d", createdEntry.Timestamp.UnixMilli())
	}

	// Fetch entry from DB and verify persisted timestamp
	fetchedEntry, err := r.GetEntry(ctx, createdDB.ID, createdEntry.ID)
	if err != nil {
		t.Fatalf("failed to get entry: %v", err)
	}
	if fetchedEntry.Timestamp.UnixMilli() < 0 {
		t.Errorf("persisted timestamp was negative epoch (year 0001): %d", fetchedEntry.Timestamp.UnixMilli())
	}
	if fetchedEntry.Timestamp.Before(before) || fetchedEntry.Timestamp.After(after) {
		t.Errorf("expected fetchedEntry.Timestamp to be around now, got %v (unix milli: %d)", fetchedEntry.Timestamp, fetchedEntry.Timestamp.UnixMilli())
	}
}

func TestGetEntries_TimeFilterEpochAndHistorical(t *testing.T) {
	ctx := context.Background()

	r, err := sqlite.NewRepository(":memory:")
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer r.Close()

	if err := r.MigrateUp(context.Background()); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	dbModel := repo.Database{
		Name:        "historical_archive",
		ContentType: "image",
	}
	createdDB, err := r.CreateDatabase(ctx, dbModel)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}

	t1950 := time.Date(1950, 1, 1, 12, 0, 0, 0, time.UTC)
	t1970 := time.Unix(0, 0).UTC() // Epoch = 0
	t2025 := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

	// Create 3 entries with specific timestamps
	_, err = r.CreateEntry(ctx, createdDB, repo.Entry{
		FileName:    "photo_1950.jpg",
		MimeType:    "image/jpeg",
		Size:        1000,
		Status:      repo.EntryStatusReady,
		Timestamp:   t1950,
		MediaFields: map[string]any{"width": 800, "height": 600},
	})
	if err != nil {
		t.Fatalf("failed to create 1950 entry: %v", err)
	}

	_, err = r.CreateEntry(ctx, createdDB, repo.Entry{
		FileName:    "photo_1970.jpg",
		MimeType:    "image/jpeg",
		Size:        2000,
		Status:      repo.EntryStatusReady,
		Timestamp:   t1970,
		MediaFields: map[string]any{"width": 800, "height": 600},
	})
	if err != nil {
		t.Fatalf("failed to create 1970 entry: %v", err)
	}

	_, err = r.CreateEntry(ctx, createdDB, repo.Entry{
		FileName:    "photo_2025.jpg",
		MimeType:    "image/jpeg",
		Size:        3000,
		Status:      repo.EntryStatusReady,
		Timestamp:   t2025,
		MediaFields: map[string]any{"width": 800, "height": 600},
	})
	if err != nil {
		t.Fatalf("failed to create 2025 entry: %v", err)
	}

	// 1. Filter with TStart = 1940 and TEnd = 1970 (Epoch 0) -> Should return 1950 and 1970 entries
	t1940 := time.Date(1940, 1, 1, 0, 0, 0, 0, time.UTC)
	results, err := r.GetEntries(ctx, createdDB.ID, repo.QueryOptions{
		TStart: t1940,
		TEnd:   t1970,
	})
	if err != nil {
		t.Fatalf("GetEntries failed: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 entries for range [1940, 1970], got %d", len(results))
	}

	byName := make(map[string]repo.Entry, len(results))
	for _, entry := range results {
		if entry.Timestamp.IsZero() {
			t.Fatalf("expected non-zero timestamp for %s, got zero time (Year 1)", entry.FileName)
		}
		if entry.Timestamp.Year() == 1 {
			t.Fatalf("expected non-Year-1 timestamp for %s, got %v", entry.FileName, entry.Timestamp)
		}
		byName[entry.FileName] = entry
	}
	e1950, ok := byName["photo_1950.jpg"]
	if !ok {
		t.Fatalf("expected photo_1950.jpg in range [1940, 1970], got %v", results)
	}
	if !e1950.Timestamp.Equal(t1950) {
		t.Errorf("expected photo_1950.jpg timestamp %v, got %v", t1950, e1950.Timestamp)
	}
	e1970, ok := byName["photo_1970.jpg"]
	if !ok {
		t.Fatalf("expected photo_1970.jpg in range [1940, 1970], got %v", results)
	}
	if !e1970.Timestamp.Equal(t1970) {
		t.Errorf("expected photo_1970.jpg timestamp %v, got %v", t1970, e1970.Timestamp)
	}

	// 2. Filter with exact epoch timestamp TStart = 1970, TEnd = 1970 -> Should return only 1970 entry
	resultsEpoch, err := r.GetEntries(ctx, createdDB.ID, repo.QueryOptions{
		TStart: t1970,
		TEnd:   t1970,
	})
	if err != nil {
		t.Fatalf("GetEntries for epoch failed: %v", err)
	}
	if len(resultsEpoch) != 1 || resultsEpoch[0].FileName != "photo_1970.jpg" {
		t.Fatalf("expected only photo_1970.jpg, got %v", resultsEpoch)
	}
	if resultsEpoch[0].Timestamp.IsZero() || resultsEpoch[0].Timestamp.Year() == 1 {
		t.Fatalf("expected non-Year-1 timestamp for photo_1970.jpg, got %v", resultsEpoch[0].Timestamp)
	}
	if !resultsEpoch[0].Timestamp.Equal(t1970) {
		t.Errorf("expected photo_1970.jpg timestamp %v, got %v", t1970, resultsEpoch[0].Timestamp)
	}
}

func TestEntryTimestamps_Pre1970AndEpochRoundTrip(t *testing.T) {
	ctx := context.Background()

	r, err := sqlite.NewRepository(":memory:")
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer r.Close()

	if err := r.MigrateUp(context.Background()); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	db, err := r.CreateDatabase(ctx, repo.Database{
		Name:        "timestamp_roundtrip_db",
		ContentType: "image",
	})
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}

	t1950 := time.Date(1950, 1, 1, 12, 0, 0, 0, time.UTC)
	t1920 := time.Date(1920, 6, 15, 8, 30, 45, 0, time.UTC)
	t1970 := time.Unix(0, 0).UTC()

	// 1. CreateEntry -> GetEntry with pre-1970 timestamp
	created, err := r.CreateEntry(ctx, db, repo.Entry{
		FileName:    "vintage_1950.jpg",
		MimeType:    "image/jpeg",
		Size:        1024,
		Status:      repo.EntryStatusReady,
		Timestamp:   t1950,
		MediaFields: map[string]any{"width": 800, "height": 600},
	})
	if err != nil {
		t.Fatalf("CreateEntry failed: %v", err)
	}
	if !created.Timestamp.Equal(t1950) {
		t.Fatalf("expected created.Timestamp == %v, got %v", t1950, created.Timestamp)
	}

	fetched, err := r.GetEntry(ctx, db.ID, created.ID)
	if err != nil {
		t.Fatalf("GetEntry failed: %v", err)
	}
	if fetched.Timestamp.IsZero() || fetched.Timestamp.Year() == 1 {
		t.Fatalf("expected non-Year-1 timestamp from GetEntry, got %v", fetched.Timestamp)
	}
	if !fetched.Timestamp.Equal(t1950) {
		t.Fatalf("expected fetched.Timestamp == %v, got %v", t1950, fetched.Timestamp)
	}

	// 2. UpdateEntryMetadata updating filename while preserving pre-1970 timestamp
	fetched.FileName = "renamed_1950.jpg"
	if _, err := r.UpdateEntryMetadata(ctx, db.ID, fetched); err != nil {
		t.Fatalf("UpdateEntryMetadata (filename) failed: %v", err)
	}
	afterRename, err := r.GetEntry(ctx, db.ID, created.ID)
	if err != nil {
		t.Fatalf("GetEntry after rename failed: %v", err)
	}
	if afterRename.FileName != "renamed_1950.jpg" {
		t.Errorf("expected FileName renamed_1950.jpg, got %s", afterRename.FileName)
	}
	if !afterRename.Timestamp.Equal(t1950) {
		t.Errorf("expected preserved timestamp %v after rename, got %v", t1950, afterRename.Timestamp)
	}

	// 3. UpdateEntryMetadata with zero time.Time{} must leave pre-1970 timestamp intact
	if _, err := r.UpdateEntryMetadata(ctx, db.ID, repo.Entry{
		ID:       created.ID,
		FileName: "zero_ts_edit.jpg",
	}); err != nil {
		t.Fatalf("UpdateEntryMetadata (zero timestamp) failed: %v", err)
	}
	afterZeroEdit, err := r.GetEntry(ctx, db.ID, created.ID)
	if err != nil {
		t.Fatalf("GetEntry after zero-timestamp edit failed: %v", err)
	}
	if afterZeroEdit.FileName != "zero_ts_edit.jpg" {
		t.Errorf("expected FileName zero_ts_edit.jpg, got %s", afterZeroEdit.FileName)
	}
	if !afterZeroEdit.Timestamp.Equal(t1950) {
		t.Errorf("expected pre-1970 timestamp %v to remain intact when UpdateEntryMetadata receives zero Time, got %v", t1950, afterZeroEdit.Timestamp)
	}

	// 4. UpdateEntryMetadata updating timestamp to another pre-1970 date
	afterZeroEdit.Timestamp = t1920
	if _, err := r.UpdateEntryMetadata(ctx, db.ID, afterZeroEdit); err != nil {
		t.Fatalf("UpdateEntryMetadata (1920) failed: %v", err)
	}
	after1920, err := r.GetEntry(ctx, db.ID, created.ID)
	if err != nil {
		t.Fatalf("GetEntry after 1920 update failed: %v", err)
	}
	if after1920.Timestamp.IsZero() || after1920.Timestamp.Year() == 1 || !after1920.Timestamp.Equal(t1920) {
		t.Errorf("expected timestamp %v, got %v", t1920, after1920.Timestamp)
	}

	// 5. UpdateEntryMetadata updating timestamp to exact Unix epoch (1970-01-01T00:00:00Z)
	after1920.Timestamp = t1970
	if _, err := r.UpdateEntryMetadata(ctx, db.ID, after1920); err != nil {
		t.Fatalf("UpdateEntryMetadata (1970 epoch) failed: %v", err)
	}
	afterEpoch, err := r.GetEntry(ctx, db.ID, created.ID)
	if err != nil {
		t.Fatalf("GetEntry after epoch update failed: %v", err)
	}
	if afterEpoch.Timestamp.IsZero() || afterEpoch.Timestamp.Year() == 1 || !afterEpoch.Timestamp.Equal(t1970) {
		t.Errorf("expected epoch timestamp %v, got %v", t1970, afterEpoch.Timestamp)
	}

	// 6. CreateEntry -> GetEntry with exact Unix epoch (0 ms)
	createdEpoch, err := r.CreateEntry(ctx, db, repo.Entry{
		FileName:    "epoch_1970.jpg",
		MimeType:    "image/jpeg",
		Size:        512,
		Status:      repo.EntryStatusReady,
		Timestamp:   t1970,
		MediaFields: map[string]any{"width": 100, "height": 100},
	})
	if err != nil {
		t.Fatalf("CreateEntry (epoch) failed: %v", err)
	}
	fetchedEpoch, err := r.GetEntry(ctx, db.ID, createdEpoch.ID)
	if err != nil {
		t.Fatalf("GetEntry (epoch) failed: %v", err)
	}
	if fetchedEpoch.Timestamp.IsZero() || fetchedEpoch.Timestamp.Year() == 1 || !fetchedEpoch.Timestamp.Equal(t1970) {
		t.Errorf("expected epoch timestamp %v, got %v", t1970, fetchedEpoch.Timestamp)
	}

	// 7. Row stored with shared.MissingTimestamp sentinel scans back as zero time.Time{}
	if _, err := r.DB.ExecContext(ctx,
		`INSERT INTO "entries_`+db.ID.String()+`" (timestamp, created_at, updated_at, filesize, preview_filesize, filename, status, mime_type, width, height) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		shared.MissingTimestamp, shared.MissingTimestamp, shared.MissingTimestamp, 128, 0, "missing_ts.jpg", repo.EntryStatusReady, "image/jpeg", 10, 10,
	); err != nil {
		t.Fatalf("failed to insert MissingTimestamp row: %v", err)
	}
	var missingID int64
	if err := r.DB.QueryRowContext(ctx, `SELECT id FROM "entries_`+db.ID.String()+`" WHERE filename = ?`, "missing_ts.jpg").Scan(&missingID); err != nil {
		t.Fatalf("failed to query missing_ts.jpg id: %v", err)
	}
	fetchedMissing, err := r.GetEntry(ctx, db.ID, missingID)
	if err != nil {
		t.Fatalf("GetEntry (MissingTimestamp) failed: %v", err)
	}
	if !fetchedMissing.Timestamp.IsZero() {
		t.Errorf("expected IsZero() == true for MissingTimestamp row, got %v", fetchedMissing.Timestamp)
	}
	if !fetchedMissing.CreatedAt.IsZero() || !fetchedMissing.UpdatedAt.IsZero() {
		t.Errorf("expected CreatedAt and UpdatedAt to be zero for MissingTimestamp row, got created=%v updated=%v", fetchedMissing.CreatedAt, fetchedMissing.UpdatedAt)
	}
}

func TestDeleteDatabase_InvalidatesCustomFieldsCache(t *testing.T) {
	ctx := context.Background()

	r, err := sqlite.NewRepository(":memory:")
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer r.Close()

	if err := r.MigrateUp(context.Background()); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	dbModel := repo.Database{
		Name:        "test_cf_db",
		ContentType: "image",
	}
	createdDB, err := r.CreateDatabase(ctx, dbModel)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}

	// Add custom field
	_, err = r.AddCustomField(ctx, createdDB.ID, repo.CustomFieldDef{
		Name: "photographer",
		Type: repo.CustomFieldTypeText,
	})
	if err != nil {
		t.Fatalf("failed to add custom field: %v", err)
	}

	// Fetch custom fields to populate cache
	fields, err := r.GetCustomFields(ctx, createdDB.ID)
	if err != nil {
		t.Fatalf("failed to get custom fields: %v", err)
	}
	if len(fields) != 1 {
		t.Fatalf("expected 1 custom field, got %d", len(fields))
	}

	// Verify cache has the key
	cacheKey := "cf:" + createdDB.ID.String()
	if _, found := r.Cache.Get(cacheKey); !found {
		t.Fatalf("expected cache key %s to be populated", cacheKey)
	}

	// Delete database
	if err := r.DeleteDatabase(ctx, createdDB.ID); err != nil {
		t.Fatalf("failed to delete database: %v", err)
	}

	// Verify cache key was evicted
	if _, found := r.Cache.Get(cacheKey); found {
		t.Fatalf("expected cache key %s to be deleted after DeleteDatabase", cacheKey)
	}
}

func TestCoordinateCustomField_SQLite(t *testing.T) {
	ctx := context.Background()

	r, err := sqlite.NewRepository(":memory:")
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer r.Close()

	if err := r.MigrateUp(context.Background()); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	dbModel := repo.Database{
		Name:        "geo_test_db",
		ContentType: "image",
	}
	db, err := r.CreateDatabase(ctx, dbModel)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}

	// 1. Add COORDINATE custom field
	addedField, err := r.AddCustomField(ctx, db.ID, repo.CustomFieldDef{
		Name:      "location",
		Type:      repo.CustomFieldTypeCoordinate,
		IsIndexed: true,
	})
	if err != nil {
		t.Fatalf("failed to add COORDINATE field: %v", err)
	}
	if addedField.Type != repo.CustomFieldTypeCoordinate {
		t.Errorf("expected type COORDINATE, got %s", addedField.Type)
	}
	db.CustomFields = append(db.CustomFields, addedField)

	// 2. Insert entries with coordinates
	e1 := repo.Entry{
		FileName: "munich.jpg",
		MimeType: "image/jpeg",
		Size:     1000,
		MediaFields: map[string]any{
			"width": 800, "height": 600,
		},
		CustomFields: map[string]any{
			"location": repo.Coordinate{Latitude: 48.137154, Longitude: 11.576124},
		},
	}
	createdE1, err := r.CreateEntry(ctx, db, e1)
	if err != nil {
		t.Fatalf("failed to create entry with coordinate: %v", err)
	}

	// Fetch entry 1
	fetchedE1, err := r.GetEntry(ctx, db.ID, createdE1.ID)
	if err != nil {
		t.Fatalf("failed to get entry: %v", err)
	}
	locVal, exists := fetchedE1.CustomFields["location"]
	if !exists {
		t.Fatalf("expected 'location' in CustomFields")
	}
	locCoord, ok := locVal.(repo.Coordinate)
	if !ok {
		t.Fatalf("expected repo.Coordinate type, got %T (%v)", locVal, locVal)
	}
	if locCoord.Latitude != 48.137154 || locCoord.Longitude != 11.576124 {
		t.Errorf("expected (48.137154, 11.576124), got (%v, %v)", locCoord.Latitude, locCoord.Longitude)
	}

	// Antimeridian entries: e2 east (+179.5), e3 west (-179.5)
	e2 := repo.Entry{
		FileName: "east.jpg",
		MimeType: "image/jpeg",
		Size:     1000,
		MediaFields: map[string]any{
			"width": 800, "height": 600,
		},
		CustomFields: map[string]any{
			"location": repo.Coordinate{Latitude: 10.0, Longitude: 179.5},
		},
	}
	createdE2, err := r.CreateEntry(ctx, db, e2)
	if err != nil {
		t.Fatalf("failed to create entry e2: %v", err)
	}

	e3 := repo.Entry{
		FileName: "west.jpg",
		MimeType: "image/jpeg",
		Size:     1000,
		MediaFields: map[string]any{
			"width": 800, "height": 600,
		},
		CustomFields: map[string]any{
			"location": repo.Coordinate{Latitude: 10.0, Longitude: -179.5},
		},
	}
	createdE3, err := r.CreateEntry(ctx, db, e3)
	if err != nil {
		t.Fatalf("failed to create entry e3: %v", err)
	}

	cfs, err := r.GetCustomFields(ctx, db.ID)
	if err != nil {
		t.Fatalf("failed to get custom fields: %v", err)
	}

	// 3. Search: standard box containing Munich
	munichSearch := repo.SearchRequest{
		Filter: &repo.FilterGroup{
			Operator: "and",
			Conditions: []repo.Condition{
				{
					Field:    "location",
					Operator: "in_box",
					Value: map[string]any{
						"min_lat": 48.0, "max_lat": 49.0,
						"min_lng": 11.0, "max_lng": 12.0,
					},
				},
			},
		},
		Pagination: repo.Pagination{Limit: 10},
	}
	res, err := r.SearchEntries(ctx, db.ID, munichSearch, cfs)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(res) != 1 || res[0].ID != createdE1.ID {
		t.Errorf("expected 1 result with ID %d, got %d results", createdE1.ID, len(res))
	}

	// Standard box outside Munich
	outsideSearch := repo.SearchRequest{
		Filter: &repo.FilterGroup{
			Operator: "and",
			Conditions: []repo.Condition{
				{
					Field:    "location",
					Operator: "in_box",
					Value: map[string]any{
						"min_lat": 50.0, "max_lat": 51.0,
						"min_lng": 11.0, "max_lng": 12.0,
					},
				},
			},
		},
		Pagination: repo.Pagination{Limit: 10},
	}
	resOutside, err := r.SearchEntries(ctx, db.ID, outsideSearch, cfs)
	if err != nil {
		t.Fatalf("search outside failed: %v", err)
	}
	if len(resOutside) != 0 {
		t.Errorf("expected 0 results, got %d", len(resOutside))
	}

	// Antimeridian search (spanning 179.0 to -179.0)
	antiSearch := repo.SearchRequest{
		Filter: &repo.FilterGroup{
			Operator: "and",
			Conditions: []repo.Condition{
				{
					Field:    "location",
					Operator: "in_box",
					Value: map[string]any{
						"min_lat": 5.0, "max_lat": 15.0,
						"min_lng": 179.0, "max_lng": -179.0,
					},
				},
			},
		},
		Pagination: repo.Pagination{Limit: 10},
	}
	resAnti, err := r.SearchEntries(ctx, db.ID, antiSearch, cfs)
	if err != nil {
		t.Fatalf("antimeridian search failed: %v", err)
	}
	if len(resAnti) != 2 {
		t.Errorf("expected 2 results across antimeridian, got %d", len(resAnti))
	}
	foundE2, foundE3 := false, false
	for _, item := range resAnti {
		if item.ID == createdE2.ID {
			foundE2 = true
		}
		if item.ID == createdE3.ID {
			foundE3 = true
		}
	}
	if !foundE2 || !foundE3 {
		t.Errorf("expected to find e2 and e3 across antimeridian, foundE2=%v, foundE3=%v", foundE2, foundE3)
	}

	// 4. Invalid operator on coordinate field
	invalidOpSearch := repo.SearchRequest{
		Filter: &repo.FilterGroup{
			Operator: "and",
			Conditions: []repo.Condition{
				{
					Field:    "location",
					Operator: "=",
					Value:    48.0,
				},
			},
		},
		Pagination: repo.Pagination{Limit: 10},
	}
	if _, err := r.SearchEntries(ctx, db.ID, invalidOpSearch, cfs); err == nil {
		t.Errorf("expected validation error for '=' operator on coordinate field")
	}

	// 5. In_box operator on non-coordinate field
	invalidFieldSearch := repo.SearchRequest{
		Filter: &repo.FilterGroup{
			Operator: "and",
			Conditions: []repo.Condition{
				{
					Field:    "filename",
					Operator: "in_box",
					Value:    map[string]any{"min_lat": 0.0, "max_lat": 1.0, "min_lng": 0.0, "max_lng": 1.0},
				},
			},
		},
		Pagination: repo.Pagination{Limit: 10},
	}
	if _, err := r.SearchEntries(ctx, db.ID, invalidFieldSearch, cfs); err == nil {
		t.Errorf("expected validation error for in_box on non-coordinate field")
	}

	// 6. Sorting by coordinate field
	sortCoordSearch := repo.SearchRequest{
		Sort: &repo.SortCriteria{
			Field:     "location",
			Direction: "asc",
		},
		Pagination: repo.Pagination{Limit: 10},
	}
	if _, err := r.SearchEntries(ctx, db.ID, sortCoordSearch, cfs); err == nil {
		t.Errorf("expected validation error when sorting by coordinate field")
	}

	// 7. Update entry coordinate
	fetchedE1.CustomFields["location"] = repo.Coordinate{Latitude: 48.2, Longitude: 11.6}
	updatedE1, err := r.UpdateEntryMetadata(ctx, db.ID, fetchedE1)
	if err != nil {
		t.Fatalf("failed to update entry: %v", err)
	}
	reFetchedE1, err := r.GetEntry(ctx, db.ID, updatedE1.ID)
	if err != nil {
		t.Fatalf("failed to re-fetch entry: %v", err)
	}
	reLoc := reFetchedE1.CustomFields["location"].(repo.Coordinate)
	if reLoc.Latitude != 48.2 || reLoc.Longitude != 11.6 {
		t.Errorf("expected updated coordinate (48.2, 11.6), got %v", reLoc)
	}

	// 8. Toggle index
	falseVal := false
	_, err = r.UpdateCustomField(ctx, db.ID, addedField.ID, nil, &falseVal)
	if err != nil {
		t.Fatalf("failed to disable index on coordinate field: %v", err)
	}
	trueVal := true
	_, err = r.UpdateCustomField(ctx, db.ID, addedField.ID, nil, &trueVal)
	if err != nil {
		t.Fatalf("failed to re-enable index on coordinate field: %v", err)
	}

	// 9. Delete custom field
	if err := r.DeleteCustomField(ctx, db.ID, addedField.ID); err != nil {
		t.Fatalf("failed to delete coordinate custom field: %v", err)
	}
	postDeleteFields, err := r.GetCustomFields(ctx, db.ID)
	if err != nil {
		t.Fatalf("failed to get fields after delete: %v", err)
	}
	if len(postDeleteFields) != 0 {
		t.Errorf("expected 0 custom fields after delete, got %d", len(postDeleteFields))
	}
}

func TestGetDatabaseULIDsWithQueuedEntries(t *testing.T) {
	ctx := context.Background()

	r, err := sqlite.NewRepository(":memory:")
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer r.Close()

	if err := r.MigrateUp(context.Background()); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	// 1. Initially no databases -> empty
	ulids, err := r.GetDatabaseULIDsWithQueuedEntries(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ulids) != 0 {
		t.Fatalf("expected 0 ulids, got %d", len(ulids))
	}

	db1, err := r.CreateDatabase(ctx, repo.Database{Name: "db1", ContentType: "file"})
	if err != nil {
		t.Fatalf("failed to create db1: %v", err)
	}
	db2, err := r.CreateDatabase(ctx, repo.Database{Name: "db2", ContentType: "file"})
	if err != nil {
		t.Fatalf("failed to create db2: %v", err)
	}

	// 2. Both databases have 0 queued entries -> still empty
	ulids, err = r.GetDatabaseULIDsWithQueuedEntries(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ulids) != 0 {
		t.Fatalf("expected 0 ulids, got %d", len(ulids))
	}

	// 3. Queue an entry in db1
	e1, err := r.CreateEntry(ctx, db1, repo.Entry{
		FileName: "e1.jpg",
		MimeType: "image/jpeg",
		Size:     100,
		Status:   repo.EntryStatusQueued,
	})
	if err != nil {
		t.Fatalf("failed to create e1: %v", err)
	}

	ulids, err = r.GetDatabaseULIDsWithQueuedEntries(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ulids) != 1 || ulids[0] != db1.ID {
		t.Fatalf("expected [db1.ID], got %v", ulids)
	}

	// 4. Queue an entry in db2
	_, err = r.CreateEntry(ctx, db2, repo.Entry{
		FileName: "e2.jpg",
		MimeType: "image/jpeg",
		Size:     200,
		Status:   repo.EntryStatusQueued,
	})
	if err != nil {
		t.Fatalf("failed to create e2: %v", err)
	}

	ulids, err = r.GetDatabaseULIDsWithQueuedEntries(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ulids) != 2 {
		t.Fatalf("expected 2 ulids, got %d", len(ulids))
	}

	// 5. Claim entry from db1
	claimed, err := r.ClaimQueuedEntry(ctx, db1.ID, e1.ID)
	if err != nil || !claimed {
		t.Fatalf("failed to claim e1: %v (claimed=%v)", err, claimed)
	}

	ulids, err = r.GetDatabaseULIDsWithQueuedEntries(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ulids) != 1 || ulids[0] != db2.ID {
		t.Fatalf("expected only db2.ID after claiming db1, got %v", ulids)
	}
}

func TestEnqueueEntry_QueueLimit(t *testing.T) {
	ctx := context.Background()

	r, err := sqlite.NewRepository(":memory:")
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer r.Close()

	if err := r.MigrateUp(context.Background()); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	db, err := r.CreateDatabase(ctx, repo.Database{Name: "db_enqueue", ContentType: "image"})
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}

	// Create 3 preliminary entries in processing state
	var prelimEntries []repo.Entry
	for i := 0; i < 3; i++ {
		e, err := r.CreateEntry(ctx, db, repo.Entry{
			FileName:    "prelim.jpg",
			MimeType:    "image/jpeg",
			Size:        0,
			Status:      repo.EntryStatusProcessing,
			MediaFields: map[string]any{"width": 0, "height": 0},
		})
		if err != nil {
			t.Fatalf("failed to create prelim entry %d: %v", i, err)
		}
		prelimEntries = append(prelimEntries, e)
	}

	// 1. maxQueued <= 0 returns ErrUnavailable and does not increment queued_count
	prelimEntries[0].Size = 1024
	_, err = r.EnqueueEntry(ctx, db.ID, prelimEntries[0], 0)
	if !errors.Is(err, customerrors.ErrUnavailable) {
		t.Fatalf("expected ErrUnavailable for maxQueued=0, got %v", err)
	}
	stats, err := r.GetDatabaseStats(ctx, db.ID)
	if err != nil {
		t.Fatalf("failed to get stats: %v", err)
	}
	if stats.QueuedCount != 0 {
		t.Fatalf("expected QueuedCount 0 after rejected enqueue, got %d", stats.QueuedCount)
	}

	// 2. Enqueue first two entries with maxQueued = 2 -> succeeds
	e0, err := r.EnqueueEntry(ctx, db.ID, prelimEntries[0], 2)
	if err != nil {
		t.Fatalf("EnqueueEntry 0 failed: %v", err)
	}
	if e0.Status != repo.EntryStatusQueued {
		t.Errorf("expected status queued, got %v", e0.Status)
	}

	prelimEntries[1].Size = 2048
	e1, err := r.EnqueueEntry(ctx, db.ID, prelimEntries[1], 2)
	if err != nil {
		t.Fatalf("EnqueueEntry 1 failed: %v", err)
	}
	if e1.Status != repo.EntryStatusQueued {
		t.Errorf("expected status queued, got %v", e1.Status)
	}

	stats, err = r.GetDatabaseStats(ctx, db.ID)
	if err != nil {
		t.Fatalf("failed to get stats: %v", err)
	}
	if stats.QueuedCount != 2 {
		t.Fatalf("expected QueuedCount 2, got %d", stats.QueuedCount)
	}
	if stats.TotalDiskSpaceBytes != 3072 {
		t.Fatalf("expected TotalDiskSpaceBytes 3072, got %d", stats.TotalDiskSpaceBytes)
	}

	// 3. Third entry exceeds maxQueued = 2 -> returns ErrUnavailable, leaves entry in processing status and stats unchanged
	prelimEntries[2].Size = 4096
	_, err = r.EnqueueEntry(ctx, db.ID, prelimEntries[2], 2)
	if !errors.Is(err, customerrors.ErrUnavailable) {
		t.Fatalf("expected ErrUnavailable when queue is full, got %v", err)
	}

	fetched2, err := r.GetEntry(ctx, db.ID, prelimEntries[2].ID)
	if err != nil {
		t.Fatalf("failed to fetch entry 2: %v", err)
	}
	if fetched2.Status != repo.EntryStatusProcessing {
		t.Errorf("expected entry 2 to remain in processing status, got %v", fetched2.Status)
	}
	if fetched2.Size != 0 {
		t.Errorf("expected entry 2 size to remain 0, got %d", fetched2.Size)
	}

	stats, err = r.GetDatabaseStats(ctx, db.ID)
	if err != nil {
		t.Fatalf("failed to get stats: %v", err)
	}
	if stats.QueuedCount != 2 {
		t.Fatalf("expected QueuedCount to remain 2, got %d", stats.QueuedCount)
	}
	if stats.TotalDiskSpaceBytes != 3072 {
		t.Fatalf("expected TotalDiskSpaceBytes to remain 3072, got %d", stats.TotalDiskSpaceBytes)
	}
}

func TestGetEntries_StatusFilter(t *testing.T) {
	ctx := context.Background()

	r, err := sqlite.NewRepository(":memory:")
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer r.Close()

	if err := r.MigrateUp(context.Background()); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	db, err := r.CreateDatabase(ctx, repo.Database{
		Name:        "test_status_filter",
		ContentType: "image",
	})
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}

	statuses := []repo.EntryStatus{
		repo.EntryStatusQueued,
		repo.EntryStatusProcessing,
		repo.EntryStatusReady,
		repo.EntryStatusError,
		repo.EntryStatusDeleting,
	}

	baseTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, st := range statuses {
		_, err := r.CreateEntry(ctx, db, repo.Entry{
			FileName:    "test.jpg",
			MimeType:    "image/jpeg",
			Size:        1024,
			Status:      st,
			Timestamp:   baseTime.Add(time.Duration(i) * time.Minute),
			MediaFields: map[string]any{"width": 800, "height": 600},
		})
		if err != nil {
			t.Fatalf("failed to create entry with status %v: %v", st, err)
		}
	}

	// Filter by Ready and Error only
	entries, err := r.GetEntries(ctx, db.ID, repo.QueryOptions{
		Order:    "asc",
		Statuses: []repo.EntryStatus{repo.EntryStatusReady, repo.EntryStatusError},
	})
	if err != nil {
		t.Fatalf("GetEntries with Statuses filter failed: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries (ready and error), got %d", len(entries))
	}
	if entries[0].Status != repo.EntryStatusReady || entries[1].Status != repo.EntryStatusError {
		t.Errorf("expected statuses [ready, error], got [%v, %v]", entries[0].Status, entries[1].Status)
	}
}

func TestCreateEntry_DuplicateExplicitIDReturnsConflict(t *testing.T) {
	ctx := context.Background()

	r, err := sqlite.NewRepository(":memory:")
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer r.Close()

	if err := r.MigrateUp(context.Background()); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	db, err := r.CreateDatabase(ctx, repo.Database{
		Name:        "dup_id_db",
		ContentType: "image",
	})
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}

	entry := repo.Entry{
		ID:          42,
		FileName:    "first.jpg",
		MimeType:    "image/jpeg",
		Size:        512,
		Status:      repo.EntryStatusReady,
		MediaFields: map[string]any{"width": 100, "height": 100},
	}
	if _, err := r.CreateEntry(ctx, db, entry); err != nil {
		t.Fatalf("failed to create first entry with explicit ID: %v", err)
	}

	entry.FileName = "second.jpg"
	_, err = r.CreateEntry(ctx, db, entry)
	if !errors.Is(err, customerrors.ErrConflict) {
		t.Fatalf("expected ErrConflict when inserting duplicate explicit ID, got: %v", err)
	}
}

func TestUpdateDatabase_ValidationAndDuplicateName(t *testing.T) {
	ctx := context.Background()

	r, err := sqlite.NewRepository(":memory:")
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer r.Close()

	if err := r.MigrateUp(context.Background()); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	db1, err := r.CreateDatabase(ctx, repo.Database{Name: "db_one", ContentType: "image"})
	if err != nil {
		t.Fatalf("failed to create db1: %v", err)
	}
	db2, err := r.CreateDatabase(ctx, repo.Database{Name: "db_two", ContentType: "image"})
	if err != nil {
		t.Fatalf("failed to create db2: %v", err)
	}

	// 1. Invalid name rejected with ErrInvalidName
	db1Invalid := db1
	db1Invalid.Name = "invalid name!"
	if _, err := r.UpdateDatabase(ctx, db1Invalid); !errors.Is(err, customerrors.ErrInvalidName) {
		t.Fatalf("expected ErrInvalidName on UpdateDatabase with invalid name, got: %v", err)
	}

	// 2. Duplicate name rejected with ErrDatabaseExists
	db2Dup := db2
	db2Dup.Name = "db_one"
	if _, err := r.UpdateDatabase(ctx, db2Dup); !errors.Is(err, customerrors.ErrDatabaseExists) {
		t.Fatalf("expected ErrDatabaseExists on UpdateDatabase with duplicate name, got: %v", err)
	}
}

func TestSearchEntries_ILIKEOperator(t *testing.T) {
	ctx := context.Background()

	r, err := sqlite.NewRepository(":memory:")
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer r.Close()

	if err := r.MigrateUp(context.Background()); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	db, err := r.CreateDatabase(ctx, repo.Database{
		Name:        "ilike_test_db",
		ContentType: "image",
	})
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}

	_, err = r.CreateEntry(ctx, db, repo.Entry{
		FileName:    "VacationPhoto.JPG",
		MimeType:    "image/jpeg",
		Size:        1024,
		Status:      repo.EntryStatusReady,
		MediaFields: map[string]any{"width": 800, "height": 600},
	})
	if err != nil {
		t.Fatalf("failed to create entry: %v", err)
	}

	req := repo.SearchRequest{
		Filter: &repo.FilterGroup{
			Operator: "and",
			Conditions: []repo.Condition{
				{
					Field:    "filename",
					Operator: "ILIKE",
					Value:    "%vacationphoto%",
				},
			},
		},
		Pagination: repo.Pagination{Limit: 10},
	}

	results, err := r.SearchEntries(ctx, db.ID, req, nil)
	if err != nil {
		t.Fatalf("SearchEntries with ILIKE failed: %v", err)
	}
	if len(results) != 1 || results[0].FileName != "VacationPhoto.JPG" {
		t.Fatalf("expected 1 matching entry for ILIKE search, got: %+v", results)
	}
}

func TestUpdateEntry_DeletingStatusReturnsNotFound(t *testing.T) {
	ctx := context.Background()

	r, err := sqlite.NewRepository(":memory:")
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer r.Close()

	if err := r.MigrateUp(context.Background()); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	db, err := r.CreateDatabase(ctx, repo.Database{
		Name:        "deleting_test_db",
		ContentType: "image",
	})
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}

	createdEntry, err := r.CreateEntry(ctx, db, repo.Entry{
		FileName:    "image.jpg",
		MimeType:    "image/jpeg",
		Size:        2048,
		Status:      repo.EntryStatusReady,
		MediaFields: map[string]any{"width": 800, "height": 600},
	})
	if err != nil {
		t.Fatalf("failed to create entry: %v", err)
	}

	// Mark entry as deleting
	err = r.UpdateEntriesStatus(ctx, db.ID, []int64{createdEntry.ID}, repo.EntryStatusDeleting)
	if err != nil {
		t.Fatalf("failed to set status to deleting: %v", err)
	}

	// Attempt to update processing fields of deleting entry -> should fail with ErrNotFound
	updateAttempt := createdEntry
	updateAttempt.Size = 4096
	_, err = r.UpdateEntry(ctx, db.ID, updateAttempt)
	if !errors.Is(err, customerrors.ErrNotFound) {
		t.Fatalf("expected ErrNotFound when updating deleting entry, got: %v", err)
	}

	// Attempt to enqueue deleting entry -> should also fail with ErrNotFound
	_, err = r.EnqueueEntry(ctx, db.ID, updateAttempt, 10)
	if !errors.Is(err, customerrors.ErrNotFound) {
		t.Fatalf("expected ErrNotFound when enqueuing deleting entry, got: %v", err)
	}
}

func TestUpdateEntriesStatus_QueuedCountTracking(t *testing.T) {
	ctx := context.Background()

	r, err := sqlite.NewRepository(":memory:")
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer r.Close()

	if err := r.MigrateUp(context.Background()); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	db, err := r.CreateDatabase(ctx, repo.Database{
		Name:        "queued_tracking_db",
		ContentType: "image",
	})
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}

	e1, err := r.CreateEntry(ctx, db, repo.Entry{
		FileName: "1.jpg", MimeType: "image/jpeg", Size: 100, Status: repo.EntryStatusReady,
		MediaFields: map[string]any{"width": 10, "height": 10},
	})
	if err != nil {
		t.Fatalf("create e1: %v", err)
	}
	e2, err := r.CreateEntry(ctx, db, repo.Entry{
		FileName: "2.jpg", MimeType: "image/jpeg", Size: 100, Status: repo.EntryStatusReady,
		MediaFields: map[string]any{"width": 10, "height": 10},
	})
	if err != nil {
		t.Fatalf("create e2: %v", err)
	}

	// Transition both to Queued
	if err := r.UpdateEntriesStatus(ctx, db.ID, []int64{e1.ID, e2.ID}, repo.EntryStatusQueued); err != nil {
		t.Fatalf("UpdateEntriesStatus to Queued: %v", err)
	}

	stats, err := r.GetDatabaseStats(ctx, db.ID)
	if err != nil {
		t.Fatalf("GetDatabaseStats: %v", err)
	}
	if stats.QueuedCount != 2 {
		t.Fatalf("expected QueuedCount == 2, got %d", stats.QueuedCount)
	}

	// Transition 1 to Processing
	if err := r.UpdateEntriesStatus(ctx, db.ID, []int64{e1.ID}, repo.EntryStatusProcessing); err != nil {
		t.Fatalf("UpdateEntriesStatus to Processing: %v", err)
	}

	stats, err = r.GetDatabaseStats(ctx, db.ID)
	if err != nil {
		t.Fatalf("GetDatabaseStats: %v", err)
	}
	if stats.QueuedCount != 1 {
		t.Fatalf("expected QueuedCount == 1, got %d", stats.QueuedCount)
	}

	// Transition non-existent ID -> should return ErrNotFound
	if err := r.UpdateEntriesStatus(ctx, db.ID, []int64{999999}, repo.EntryStatusReady); !errors.Is(err, customerrors.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for non-existent IDs, got: %v", err)
	}
}

func TestSQLite_MigrationLifecycle(t *testing.T) {
	ctx := context.Background()

	r, err := sqlite.NewRepository(":memory:")
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer r.Close()

	// 1. Fresh DB returns version 0 before MigrateUp
	version, err := r.GetMigrationVersion(ctx)
	if err != nil {
		t.Fatalf("GetMigrationVersion on fresh DB failed: %v", err)
	}
	if version != 0 {
		t.Fatalf("expected initial migration version 0, got %d", version)
	}

	// 2. MigrateUp advances to RequiredVersion
	if err := r.MigrateUp(ctx); err != nil {
		t.Fatalf("MigrateUp failed: %v", err)
	}
	version, err = r.GetMigrationVersion(ctx)
	if err != nil {
		t.Fatalf("GetMigrationVersion after MigrateUp failed: %v", err)
	}
	if version != migrations.RequiredVersion {
		t.Fatalf("expected migration version %d after MigrateUp, got %d", migrations.RequiredVersion, version)
	}

	// 3. MigrateDown rolls back below RequiredVersion
	if err := r.MigrateDown(ctx); err != nil {
		t.Fatalf("MigrateDown failed: %v", err)
	}
	version, err = r.GetMigrationVersion(ctx)
	if err != nil {
		t.Fatalf("GetMigrationVersion after MigrateDown failed: %v", err)
	}
	if version >= migrations.RequiredVersion {
		t.Fatalf("expected migration version < %d after MigrateDown, got %d", migrations.RequiredVersion, version)
	}
}

func TestSQLite_GetEntries_IDRange(t *testing.T) {
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
		Name:        "id_range_db",
		ContentType: "image",
	})
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}

	for i := 1; i <= 5; i++ {
		if _, err := r.CreateEntry(ctx, db, repo.Entry{
			ID:          int64(i),
			FileName:    "img.jpg",
			MimeType:    "image/jpeg",
			Size:        100,
			Status:      repo.EntryStatusReady,
			MediaFields: map[string]any{"width": 100, "height": 100},
		}); err != nil {
			t.Fatalf("CreateEntry %d failed: %v", i, err)
		}
	}

	checkIDs := func(name string, opts repo.QueryOptions, want []int64) {
		t.Helper()
		entries, err := r.GetEntries(ctx, db.ID, opts)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", name, err)
		}
		if len(entries) != len(want) {
			t.Fatalf("%s: got %d entries, want %d", name, len(entries), len(want))
		}
		for i, w := range want {
			if entries[i].ID != w {
				t.Errorf("%s: entries[%d].ID = %d, want %d", name, i, entries[i].ID, w)
			}
		}
	}

	checkIDs("idstart only", repo.QueryOptions{Order: "asc", IDStart: 3}, []int64{3, 4, 5})
	checkIDs("idend only", repo.QueryOptions{Order: "asc", IDEnd: 2}, []int64{1, 2})
	checkIDs("both inclusive", repo.QueryOptions{Order: "asc", IDStart: 2, IDEnd: 4}, []int64{2, 3, 4})
	checkIDs("single id", repo.QueryOptions{Order: "asc", IDStart: 3, IDEnd: 3}, []int64{3})

	if _, err := r.GetEntries(ctx, db.ID, repo.QueryOptions{IDStart: 4, IDEnd: 2}); err == nil {
		t.Fatalf("expected error for inverted IDStart/IDEnd range, got nil")
	}
}

func TestGetEntryPreviewState(t *testing.T) {
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
		Name:        "preview_state_db",
		ContentType: "image",
	})
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}

	// 1. Missing database -> ErrNotFound
	missingDBID := ulid.New()
	if _, err := r.GetEntryPreviewState(ctx, missingDBID, 1); !errors.Is(err, customerrors.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for missing DB, got: %v", err)
	}

	// 2. Missing entry -> ErrNotFound
	if _, err := r.GetEntryPreviewState(ctx, db.ID, 99999); !errors.Is(err, customerrors.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for missing entry, got: %v", err)
	}

	// 3. Valid entry state
	created, err := r.CreateEntry(ctx, db, repo.Entry{
		FileName:    "photo.jpg",
		MimeType:    "image/jpeg",
		Size:        2048,
		PreviewSize: 512,
		Status:      repo.EntryStatusReady,
		MediaFields: map[string]any{"width": 800, "height": 600},
	})
	if err != nil {
		t.Fatalf("CreateEntry failed: %v", err)
	}

	state, err := r.GetEntryPreviewState(ctx, db.ID, created.ID)
	if err != nil {
		t.Fatalf("GetEntryPreviewState failed: %v", err)
	}
	if state.Status != repo.EntryStatusReady {
		t.Errorf("expected Status %v, got %v", repo.EntryStatusReady, state.Status)
	}
	if state.PreviewSize != 512 {
		t.Errorf("expected PreviewSize 512, got %d", state.PreviewSize)
	}
	if state.UpdatedAt.IsZero() || state.UpdatedAt.UnixMilli() != created.UpdatedAt.UnixMilli() {
		t.Errorf("expected UpdatedAt %v, got %v", created.UpdatedAt, state.UpdatedAt)
	}
}

func TestUpdateEntryPreviewSize(t *testing.T) {
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
		Name:        "preview_update_db",
		ContentType: "image",
	})
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}

	// 1. Missing DB -> ErrNotFound
	missingDBID := ulid.New()
	if _, _, err := r.UpdateEntryPreviewSize(ctx, missingDBID, 1, 500, repo.PreviewUpdateUpsert); !errors.Is(err, customerrors.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for missing DB, got: %v", err)
	}

	// 2. Missing entry -> ErrNotFound
	if _, _, err := r.UpdateEntryPreviewSize(ctx, db.ID, 99999, 500, repo.PreviewUpdateUpsert); !errors.Is(err, customerrors.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for missing entry, got: %v", err)
	}

	// Create main entry with size 1000 and preview_filesize 0
	entry, err := r.CreateEntry(ctx, db, repo.Entry{
		FileName:    "img.jpg",
		MimeType:    "image/jpeg",
		Size:        1000,
		PreviewSize: 0,
		Status:      repo.EntryStatusReady,
		MediaFields: map[string]any{"width": 100, "height": 100},
	})
	if err != nil {
		t.Fatalf("CreateEntry failed: %v", err)
	}

	assertDiskSpace := func(want uint64) {
		t.Helper()
		stats, err := r.GetDatabaseStats(ctx, db.ID)
		if err != nil {
			t.Fatalf("GetDatabaseStats failed: %v", err)
		}
		if stats.TotalDiskSpaceBytes != want {
			t.Fatalf("TotalDiskSpaceBytes = %d, want %d", stats.TotalDiskSpaceBytes, want)
		}
	}

	assertDiskSpace(1000)

	// 3. PreviewUpdateCreateOnly: 0 -> 500 succeeds (+500 disk)
	prevUpdated := entry.UpdatedAt
	time.Sleep(5 * time.Millisecond)

	state, oldSize, err := r.UpdateEntryPreviewSize(ctx, db.ID, entry.ID, 500, repo.PreviewUpdateCreateOnly)
	if err != nil {
		t.Fatalf("CreateOnly 0->500 failed: %v", err)
	}
	if oldSize != 0 || state.PreviewSize != 500 || state.Status != repo.EntryStatusReady {
		t.Fatalf("unexpected state/oldSize after CreateOnly: state=%+v, oldSize=%d", state, oldSize)
	}
	if !state.UpdatedAt.After(prevUpdated) {
		t.Errorf("expected UpdatedAt (%v) to advance past %v", state.UpdatedAt, prevUpdated)
	}
	assertDiskSpace(1500)

	// Calling CreateOnly again (500 -> 600) returns ErrConflict without modifying DB or stats
	if _, _, err := r.UpdateEntryPreviewSize(ctx, db.ID, entry.ID, 600, repo.PreviewUpdateCreateOnly); !errors.Is(err, customerrors.ErrConflict) {
		t.Fatalf("expected ErrConflict on CreateOnly when preview already exists, got: %v", err)
	}
	assertDiskSpace(1500)
	gotState, err := r.GetEntryPreviewState(ctx, db.ID, entry.ID)
	if err != nil {
		t.Fatalf("GetEntryPreviewState failed: %v", err)
	}
	if gotState.PreviewSize != 500 || gotState.UpdatedAt.UnixMilli() != state.UpdatedAt.UnixMilli() {
		t.Fatalf("expected unchanged state after conflict, got: %+v", gotState)
	}

	// 4. PreviewUpdateUpsert: 500 -> 800 (+300 disk)
	prevUpdated = state.UpdatedAt
	time.Sleep(5 * time.Millisecond)
	state, oldSize, err = r.UpdateEntryPreviewSize(ctx, db.ID, entry.ID, 800, repo.PreviewUpdateUpsert)
	if err != nil {
		t.Fatalf("Upsert 500->800 failed: %v", err)
	}
	if oldSize != 500 || state.PreviewSize != 800 {
		t.Fatalf("unexpected state/oldSize after Upsert 500->800: state=%+v, oldSize=%d", state, oldSize)
	}
	if !state.UpdatedAt.After(prevUpdated) {
		t.Errorf("expected UpdatedAt (%v) to advance past %v", state.UpdatedAt, prevUpdated)
	}
	assertDiskSpace(1800)

	// PreviewUpdateUpsert: 800 -> 300 (-500 disk)
	state, oldSize, err = r.UpdateEntryPreviewSize(ctx, db.ID, entry.ID, 300, repo.PreviewUpdateUpsert)
	if err != nil {
		t.Fatalf("Upsert 800->300 failed: %v", err)
	}
	if oldSize != 800 || state.PreviewSize != 300 {
		t.Fatalf("unexpected state/oldSize after Upsert 800->300: state=%+v, oldSize=%d", state, oldSize)
	}
	assertDiskSpace(1300)

	// 5. PreviewUpdateDeleteOnly: 300 -> 0 (-300 disk)
	prevUpdated = state.UpdatedAt
	time.Sleep(5 * time.Millisecond)
	state, oldSize, err = r.UpdateEntryPreviewSize(ctx, db.ID, entry.ID, 0, repo.PreviewUpdateDeleteOnly)
	if err != nil {
		t.Fatalf("DeleteOnly 300->0 failed: %v", err)
	}
	if oldSize != 300 || state.PreviewSize != 0 {
		t.Fatalf("unexpected state/oldSize after DeleteOnly 300->0: state=%+v, oldSize=%d", state, oldSize)
	}
	if !state.UpdatedAt.After(prevUpdated) {
		t.Errorf("expected UpdatedAt (%v) to advance past %v", state.UpdatedAt, prevUpdated)
	}
	assertDiskSpace(1000)

	// Calling DeleteOnly again (0 -> 0) returns ErrNotFound
	if _, _, err := r.UpdateEntryPreviewSize(ctx, db.ID, entry.ID, 0, repo.PreviewUpdateDeleteOnly); !errors.Is(err, customerrors.ErrNotFound) {
		t.Fatalf("expected ErrNotFound on DeleteOnly when preview_filesize == 0, got: %v", err)
	}
	assertDiskSpace(1000)

	// PreviewUpdateUpsert from 0 -> 500 creates (+500 disk)
	state, oldSize, err = r.UpdateEntryPreviewSize(ctx, db.ID, entry.ID, 500, repo.PreviewUpdateUpsert)
	if err != nil {
		t.Fatalf("Upsert 0->500 failed: %v", err)
	}
	if oldSize != 0 || state.PreviewSize != 500 {
		t.Fatalf("unexpected state/oldSize after Upsert 0->500: state=%+v, oldSize=%d", state, oldSize)
	}
	assertDiskSpace(1500)

	// 6. Status guards: Processing, Queued, Deleting return ErrConflict across all modes
	blockedStatuses := []repo.EntryStatus{
		repo.EntryStatusProcessing,
		repo.EntryStatusQueued,
		repo.EntryStatusDeleting,
	}
	modes := []struct {
		name string
		mode repo.PreviewUpdateMode
		size uint64
	}{
		{"CreateOnly", repo.PreviewUpdateCreateOnly, 200},
		{"Upsert", repo.PreviewUpdateUpsert, 200},
		{"DeleteOnly", repo.PreviewUpdateDeleteOnly, 0},
	}

	for _, st := range blockedStatuses {
		if err := r.UpdateEntriesStatus(ctx, db.ID, []int64{entry.ID}, st); err != nil {
			t.Fatalf("UpdateEntriesStatus(%v) failed: %v", st, err)
		}
		for _, m := range modes {
			if _, _, err := r.UpdateEntryPreviewSize(ctx, db.ID, entry.ID, m.size, m.mode); !errors.Is(err, customerrors.ErrConflict) {
				t.Fatalf("status=%v mode=%s: expected ErrConflict, got %v", st, m.name, err)
			}
		}
	}

	// 7. EntryStatusError is allowed
	if err := r.UpdateEntriesStatus(ctx, db.ID, []int64{entry.ID}, repo.EntryStatusError); err != nil {
		t.Fatalf("UpdateEntriesStatus(Error) failed: %v", err)
	}
	state, oldSize, err = r.UpdateEntryPreviewSize(ctx, db.ID, entry.ID, 700, repo.PreviewUpdateUpsert)
	if err != nil {
		t.Fatalf("UpdateEntryPreviewSize with EntryStatusError failed: %v", err)
	}
	if oldSize != 500 || state.PreviewSize != 700 || state.Status != repo.EntryStatusError {
		t.Fatalf("unexpected result for EntryStatusError: state=%+v, oldSize=%d", state, oldSize)
	}
	assertDiskSpace(1700)
}
