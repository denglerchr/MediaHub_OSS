package repository

import (
	"fmt"
	"strings"
	"time"
)

// MaxQueryLimit caps the number of records returned by a single paginated query.
const MaxQueryLimit = 1000

// QueryOptions defines generic parameters for pagination, sorting, and time-based filtering.
type QueryOptions struct {
	Limit     int
	Offset    int
	Order     string        // "asc" or "desc"
	SortBy    string        // e.g., "timestamp", "created_at", "updated_at", "id"
	TimeField string        // e.g., "timestamp", "created_at", "updated_at"
	TStart    time.Time
	TEnd      time.Time
	Statuses  []EntryStatus // Optional status filter (e.g. ready, error)
	IDStart   int64         // Inclusive lower ID bound (id >= IDStart); 0 means unset
	IDEnd     int64         // Inclusive upper ID bound (id <= IDEnd); 0 means unset
}

// Validate checks query options, assigns defaults for missing values, and returns an error if any parameter is invalid.
func (o *QueryOptions) Validate() error {
	if o.IDStart < 0 {
		return fmt.Errorf("idstart cannot be negative")
	}
	if o.IDEnd < 0 {
		return fmt.Errorf("idend cannot be negative")
	}
	if o.IDStart > 0 && o.IDEnd > 0 && o.IDStart > o.IDEnd {
		return fmt.Errorf("idstart (%d) cannot be greater than idend (%d)", o.IDStart, o.IDEnd)
	}
	if o.Limit <= 0 {
		o.Limit = 30
	} else if o.Limit > MaxQueryLimit {
		return fmt.Errorf("limit cannot exceed %d", MaxQueryLimit)
	}
	if o.Offset < 0 {
		o.Offset = 0
	}

	if o.Order == "" {
		o.Order = "desc"
	} else {
		o.Order = strings.ToLower(o.Order)
		if o.Order != "asc" && o.Order != "desc" {
			return fmt.Errorf("invalid order: %q (must be asc or desc)", o.Order)
		}
	}

	if o.SortBy == "" {
		o.SortBy = "timestamp"
	} else {
		o.SortBy = strings.ToLower(o.SortBy)
		if o.SortBy != "timestamp" && o.SortBy != "created_at" && o.SortBy != "updated_at" && o.SortBy != "id" {
			return fmt.Errorf("invalid sort_by: %q (must be timestamp, created_at, updated_at, or id)", o.SortBy)
		}
	}

	if o.TimeField == "" {
		o.TimeField = "timestamp"
	} else {
		o.TimeField = strings.ToLower(o.TimeField)
		if o.TimeField != "timestamp" && o.TimeField != "created_at" && o.TimeField != "updated_at" {
			return fmt.Errorf("invalid time_field: %q (must be timestamp, created_at, or updated_at)", o.TimeField)
		}
	}

	return nil
}
