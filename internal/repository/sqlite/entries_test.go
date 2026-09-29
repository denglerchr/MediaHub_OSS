package sqlite_test

import (
	"context"
	"testing"
	"time"

	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/repository/migrations"
	_ "mediahub_oss/internal/repository/migrations/sqlite"
	"mediahub_oss/internal/repository/sqlite"

	"github.com/pressly/goose/v3"
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
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("failed to set goose dialect: %v", err)
	}
	goose.SetBaseFS(migrations.EmbedFS)
	if err := goose.Up(r.DB, "sqlite"); err != nil {
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

	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("failed to set goose dialect: %v", err)
	}
	goose.SetBaseFS(migrations.EmbedFS)
	if err := goose.Up(r.DB, "sqlite"); err != nil {
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
}

func TestDeleteDatabase_InvalidatesCustomFieldsCache(t *testing.T) {
	ctx := context.Background()

	r, err := sqlite.NewRepository(":memory:")
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer r.Close()

	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("failed to set goose dialect: %v", err)
	}
	goose.SetBaseFS(migrations.EmbedFS)
	if err := goose.Up(r.DB, "sqlite"); err != nil {
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

	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("failed to set goose dialect: %v", err)
	}
	goose.SetBaseFS(migrations.EmbedFS)
	if err := goose.Up(r.DB, "sqlite"); err != nil {
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
	updatedE1, err := r.UpdateEntry(ctx, db.ID, fetchedE1)
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

func TestGetEntriesByStatus_Limit(t *testing.T) {
	ctx := context.Background()

	r, err := sqlite.NewRepository(":memory:")
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer r.Close()

	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("failed to set goose dialect: %v", err)
	}
	goose.SetBaseFS(migrations.EmbedFS)
	if err := goose.Up(r.DB, "sqlite"); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	db, err := r.CreateDatabase(ctx, repo.Database{
		Name:        "test_queue_limit",
		ContentType: "image",
	})
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}

	// Create 5 queued entries
	for i := 0; i < 5; i++ {
		_, err := r.CreateEntry(ctx, db, repo.Entry{
			FileName: "queued.jpg",
			MimeType: "image/jpeg",
			Size:     1024,
			Status:   repo.EntryStatusQueued,
			MediaFields: map[string]any{
				"width":  800,
				"height": 600,
			},
		})
		if err != nil {
			t.Fatalf("failed to create entry %d: %v", i, err)
		}
	}

	// 1. With 0 limit -> returns all 5
	all, err := r.GetEntriesByStatus(ctx, db.ID, repo.EntryStatusQueued, 0)
	if err != nil {
		t.Fatalf("GetEntriesByStatus without limit failed: %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("expected 5 entries without limit, got %d", len(all))
	}

	// 2. With limit 1 -> returns oldest 1
	one, err := r.GetEntriesByStatus(ctx, db.ID, repo.EntryStatusQueued, 1)
	if err != nil {
		t.Fatalf("GetEntriesByStatus with limit 1 failed: %v", err)
	}
	if len(one) != 1 {
		t.Fatalf("expected 1 entry with limit 1, got %d", len(one))
	}
	if one[0].ID != all[0].ID {
		t.Errorf("expected oldest entry ID %d, got %d", all[0].ID, one[0].ID)
	}

	// 3. With limit 3 -> returns oldest 3
	three, err := r.GetEntriesByStatus(ctx, db.ID, repo.EntryStatusQueued, 3)
	if err != nil {
		t.Fatalf("GetEntriesByStatus with limit 3 failed: %v", err)
	}
	if len(three) != 3 {
		t.Fatalf("expected 3 entries with limit 3, got %d", len(three))
	}
}
