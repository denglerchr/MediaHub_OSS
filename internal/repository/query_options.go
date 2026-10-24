package repository

import (
	"fmt"
	"strings"
	"time"
)

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
}

// Validate checks query options, assigns defaults for missing values, and returns an error if any parameter is invalid.
func (o *QueryOptions) Validate() error {
	if o.Limit <= 0 {
		o.Limit = 30
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
