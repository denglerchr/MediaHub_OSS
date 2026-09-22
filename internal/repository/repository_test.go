package repository_test

import (
	"context"
	"errors"
	"testing"

	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared/customerrors"
)

// mockUserRepo implements only the repository methods needed for UserExists tests.
type mockUserRepo struct {
	repo.Repository
	getUserByUsernameFunc func(ctx context.Context, username string) (repo.User, error)
}

func (m *mockUserRepo) GetUserByUsername(ctx context.Context, username string) (repo.User, error) {
	if m.getUserByUsernameFunc != nil {
		return m.getUserByUsernameFunc(ctx, username)
	}
	return repo.User{}, customerrors.ErrNotFound
}

func TestUserExists(t *testing.T) {
	ctx := context.Background()

	t.Run("user exists", func(t *testing.T) {
		m := &mockUserRepo{
			getUserByUsernameFunc: func(ctx context.Context, username string) (repo.User, error) {
				return repo.User{Username: username}, nil
			},
		}
		exists, err := repo.UserExists(ctx, m, "alice")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !exists {
			t.Fatalf("expected exists to be true, got false")
		}
	})

	t.Run("user does not exist - ErrNotFound", func(t *testing.T) {
		m := &mockUserRepo{
			getUserByUsernameFunc: func(ctx context.Context, username string) (repo.User, error) {
				return repo.User{}, customerrors.ErrNotFound
			},
		}
		exists, err := repo.UserExists(ctx, m, "bob")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if exists {
			t.Fatalf("expected exists to be false, got true")
		}
	})

	t.Run("unexpected repository error", func(t *testing.T) {
		expectedErr := errors.New("db connection timeout")
		m := &mockUserRepo{
			getUserByUsernameFunc: func(ctx context.Context, username string) (repo.User, error) {
				return repo.User{}, expectedErr
			},
		}
		exists, err := repo.UserExists(ctx, m, "dave")
		if !errors.Is(err, expectedErr) {
			t.Fatalf("expected error %v, got %v", expectedErr, err)
		}
		if exists {
			t.Fatalf("expected exists to be false on error")
		}
	})
}

func TestParseCoordinate(t *testing.T) {
	// Valid struct
	c1 := repo.Coordinate{Latitude: 48.137, Longitude: 11.576}
	parsed, err := repo.ParseCoordinate(c1)
	if err != nil || parsed != c1 {
		t.Fatalf("expected %v, got %v, err: %v", c1, parsed, err)
	}

	// Valid map
	m := map[string]any{"latitude": 48.137, "longitude": 11.576}
	parsed, err = repo.ParseCoordinate(m)
	if err != nil || parsed.Latitude != 48.137 || parsed.Longitude != 11.576 {
		t.Fatalf("expected %v, got %v, err: %v", c1, parsed, err)
	}

	// Valid string numbers in map
	mStr := map[string]any{"latitude": "48.137", "longitude": "11.576"}
	parsed, err = repo.ParseCoordinate(mStr)
	if err != nil || parsed.Latitude != 48.137 || parsed.Longitude != 11.576 {
		t.Fatalf("expected %v, got %v, err: %v", c1, parsed, err)
	}

	// Valid map with 'lat' and 'lng'
	mShort := map[string]any{"lat": 48.137, "lng": 11.576}
	parsed, err = repo.ParseCoordinate(mShort)
	if err != nil || parsed.Latitude != 48.137 || parsed.Longitude != 11.576 {
		t.Fatalf("expected %v, got %v, err: %v", c1, parsed, err)
	}

	// Valid map with 'lat' and 'lon'
	mLon := map[string]any{"lat": 48.137, "lon": 11.576}
	parsed, err = repo.ParseCoordinate(mLon)
	if err != nil || parsed.Latitude != 48.137 || parsed.Longitude != 11.576 {
		t.Fatalf("expected %v, got %v, err: %v", c1, parsed, err)
	}

	// Case-insensitive keys
	mCase := map[string]any{"LAT": 48.137, "Lng": 11.576}
	parsed, err = repo.ParseCoordinate(mCase)
	if err != nil || parsed.Latitude != 48.137 || parsed.Longitude != 11.576 {
		t.Fatalf("expected %v, got %v, err: %v", c1, parsed, err)
	}

	// map[string]float64
	mFloat := map[string]float64{"lat": 48.137, "lng": 11.576}
	parsed, err = repo.ParseCoordinate(mFloat)
	if err != nil || parsed.Latitude != 48.137 || parsed.Longitude != 11.576 {
		t.Fatalf("expected %v, got %v, err: %v", c1, parsed, err)
	}

	// Nil
	if _, err := repo.ParseCoordinate(nil); err == nil {
		t.Errorf("expected error for nil coordinate")
	}

	// Out of bounds lat
	if _, err := repo.ParseCoordinate(map[string]any{"latitude": 91.0, "longitude": 0.0}); err == nil {
		t.Errorf("expected error for latitude > 90")
	}
	if _, err := repo.ParseCoordinate(map[string]any{"latitude": -91.0, "longitude": 0.0}); err == nil {
		t.Errorf("expected error for latitude < -90")
	}

	// Out of bounds lng
	if _, err := repo.ParseCoordinate(map[string]any{"latitude": 0.0, "longitude": 181.0}); err == nil {
		t.Errorf("expected error for longitude > 180")
	}
	if _, err := repo.ParseCoordinate(map[string]any{"latitude": 0.0, "longitude": -181.0}); err == nil {
		t.Errorf("expected error for longitude < -180")
	}

	// Missing field
	if _, err := repo.ParseCoordinate(map[string]any{"latitude": 48.0}); err == nil {
		t.Errorf("expected error for missing longitude")
	}
	if _, err := repo.ParseCoordinate(map[string]any{"lat": 48.0}); err == nil {
		t.Errorf("expected error for missing longitude")
	}
}

func TestParseBoundingBox(t *testing.T) {
	// Valid standard
	b1 := repo.BoundingBox{MinLat: 40.0, MaxLat: 50.0, MinLng: 10.0, MaxLng: 20.0}
	parsed, err := repo.ParseBoundingBox(b1)
	if err != nil || parsed != b1 {
		t.Fatalf("expected %v, got %v, err: %v", b1, parsed, err)
	}

	// Valid antimeridian crossing (MinLng > MaxLng)
	bCross := repo.BoundingBox{MinLat: 40.0, MaxLat: 50.0, MinLng: 170.0, MaxLng: -170.0}
	parsed, err = repo.ParseBoundingBox(bCross)
	if err != nil || parsed != bCross {
		t.Fatalf("expected %v, got %v, err: %v", bCross, parsed, err)
	}

	// Valid map
	m := map[string]any{"min_lat": 40.0, "max_lat": 50.0, "min_lng": 10.0, "max_lng": 20.0}
	parsed, err = repo.ParseBoundingBox(m)
	if err != nil || parsed != b1 {
		t.Fatalf("expected %v, got %v, err: %v", b1, parsed, err)
	}

	// MinLat > MaxLat
	if _, err := repo.ParseBoundingBox(repo.BoundingBox{MinLat: 60.0, MaxLat: 50.0, MinLng: 10.0, MaxLng: 20.0}); err == nil {
		t.Errorf("expected error for min_lat > max_lat")
	}

	// Out of bounds
	if _, err := repo.ParseBoundingBox(repo.BoundingBox{MinLat: -95.0, MaxLat: 50.0, MinLng: 10.0, MaxLng: 20.0}); err == nil {
		t.Errorf("expected error for min_lat < -90")
	}
	if _, err := repo.ParseBoundingBox(repo.BoundingBox{MinLat: 40.0, MaxLat: 95.0, MinLng: 10.0, MaxLng: 20.0}); err == nil {
		t.Errorf("expected error for max_lat > 90")
	}
	if _, err := repo.ParseBoundingBox(repo.BoundingBox{MinLat: 40.0, MaxLat: 50.0, MinLng: -190.0, MaxLng: 20.0}); err == nil {
		t.Errorf("expected error for min_lng < -180")
	}
	if _, err := repo.ParseBoundingBox(repo.BoundingBox{MinLat: 40.0, MaxLat: 50.0, MinLng: 10.0, MaxLng: 190.0}); err == nil {
		t.Errorf("expected error for max_lng > 180")
	}
}
