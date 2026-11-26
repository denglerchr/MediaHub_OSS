package repository_test

import (
	"math"
	"strings"
	"testing"

	repo "mediahub_oss/internal/repository"
)

func TestParseCoordinate_RejectsNaNAndInf(t *testing.T) {
	badInputs := []struct {
		name string
		val  any
	}{
		{"struct NaN lat", repo.Coordinate{Latitude: math.NaN(), Longitude: 10.0}},
		{"struct NaN lng", repo.Coordinate{Latitude: 10.0, Longitude: math.NaN()}},
		{"struct +Inf lat", repo.Coordinate{Latitude: math.Inf(1), Longitude: 10.0}},
		{"struct -Inf lng", repo.Coordinate{Latitude: 10.0, Longitude: math.Inf(-1)}},
		{"map float NaN lat", map[string]any{"latitude": math.NaN(), "longitude": 10.0}},
		{"map float +Inf lng", map[string]any{"latitude": 10.0, "longitude": math.Inf(1)}},
		{"map string NaN lat", map[string]any{"latitude": "NaN", "longitude": "10.0"}},
		{"map string Inf lat", map[string]any{"latitude": "Inf", "longitude": "10.0"}},
		{"map string -Inf lng", map[string]any{"latitude": "10.0", "longitude": "-Inf"}},
		{"map string +Inf lng", map[string]any{"latitude": "10.0", "longitude": "+Inf"}},
	}

	for _, tc := range badInputs {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := repo.ParseCoordinate(tc.val); err == nil {
				t.Errorf("expected error for %s, got nil", tc.name)
			}
		})
	}
}

func TestParseBoundingBox_RejectsNaNAndInf(t *testing.T) {
	badInputs := []struct {
		name string
		val  any
	}{
		{"struct NaN min_lat", repo.BoundingBox{MinLat: math.NaN(), MaxLat: 10, MinLng: 0, MaxLng: 10}},
		{"struct NaN max_lat", repo.BoundingBox{MinLat: 0, MaxLat: math.NaN(), MinLng: 0, MaxLng: 10}},
		{"struct NaN min_lng", repo.BoundingBox{MinLat: 0, MaxLat: 10, MinLng: math.NaN(), MaxLng: 10}},
		{"struct NaN max_lng", repo.BoundingBox{MinLat: 0, MaxLat: 10, MinLng: 0, MaxLng: math.NaN()}},
		{"struct +Inf min_lat", repo.BoundingBox{MinLat: math.Inf(1), MaxLat: 10, MinLng: 0, MaxLng: 10}},
		{"struct -Inf max_lng", repo.BoundingBox{MinLat: 0, MaxLat: 10, MinLng: 0, MaxLng: math.Inf(-1)}},
		{"map string NaN", map[string]any{"min_lat": "NaN", "max_lat": "10", "min_lng": "0", "max_lng": "10"}},
		{"map string Inf", map[string]any{"min_lat": "0", "max_lat": "Inf", "min_lng": "0", "max_lng": "10"}},
		{"map string -Inf", map[string]any{"min_lat": "0", "max_lat": "10", "min_lng": "-Inf", "max_lng": "10"}},
	}

	for _, tc := range badInputs {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := repo.ParseBoundingBox(tc.val); err == nil {
				t.Errorf("expected error for %s, got nil", tc.name)
			}
		})
	}
}

func TestQueryOptions_MaxQueryLimit(t *testing.T) {
	okOpts := repo.QueryOptions{Limit: repo.MaxQueryLimit}
	if err := okOpts.Validate(); err != nil {
		t.Fatalf("expected limit %d to be valid, got: %v", repo.MaxQueryLimit, err)
	}

	tooHigh := repo.QueryOptions{Limit: repo.MaxQueryLimit + 1}
	if err := tooHigh.Validate(); err == nil {
		t.Fatalf("expected error for limit > %d, got nil", repo.MaxQueryLimit)
	}
}

func TestQueryOptions_IDRangeValidation(t *testing.T) {
	valid := []repo.QueryOptions{
		{},
		{IDStart: 5},
		{IDEnd: 10},
		{IDStart: 5, IDEnd: 5},
		{IDStart: 5, IDEnd: 10},
	}
	for _, opts := range valid {
		if err := opts.Validate(); err != nil {
			t.Errorf("expected %+v to validate, got error: %v", opts, err)
		}
	}

	invalid := []repo.QueryOptions{
		{IDStart: -1},
		{IDEnd: -1},
		{IDStart: 10, IDEnd: 5},
	}
	for _, opts := range invalid {
		if err := opts.Validate(); err == nil {
			t.Errorf("expected %+v to fail validation, got nil", opts)
		}
	}
}

func TestOIDCHelpers_EscapeAndTruncate(t *testing.T) {
	if got := repo.EscapeLikePattern(`a_b%c\d`); got != `a\_b\%c\\d` {
		t.Errorf("unexpected EscapeLikePattern result: %q", got)
	}

	longASCII := strings.Repeat("x", 80)
	base := repo.ExtractUsernameBase(longASCII, "")
	if len(base) != repo.MaxUsernameLength {
		t.Errorf("expected truncated length %d, got %d", repo.MaxUsernameLength, len(base))
	}

	// Multi-byte UTF-8 boundary check: 33 * 2-byte rune = 66 bytes -> truncated to 64 bytes (32 runes)
	multiByte := strings.Repeat("ä", 33)
	baseUTF8 := repo.ExtractUsernameBase(multiByte, "")
	if len(baseUTF8) > repo.MaxUsernameLength {
		t.Errorf("expected UTF-8 truncated byte len <= %d, got %d", repo.MaxUsernameLength, len(baseUTF8))
	}

	resolved := repo.ResolveUniqueUsername(base, []string{base, base[:62] + "_2"})
	if len(resolved) > repo.MaxUsernameLength {
		t.Errorf("expected resolved username <= %d, got %d (%q)", repo.MaxUsernameLength, len(resolved), resolved)
	}
	if resolved != base[:62]+"_3" {
		t.Errorf("expected %q, got %q", base[:62]+"_3", resolved)
	}
}
