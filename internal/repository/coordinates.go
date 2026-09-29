package repository

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Coordinate represents a geographic location (latitude and longitude).
type Coordinate struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

// BoundingBox defines a rectangular geographic area for spatial searches.
type BoundingBox struct {
	MinLat float64 `json:"min_lat"`
	MaxLat float64 `json:"max_lat"`
	MinLng float64 `json:"min_lng"`
	MaxLng float64 `json:"max_lng"`
}

// used to convert parsed coordinate values to float64, handling various types and string representations
func toFloat64(val any) (float64, bool) {
	switch v := val.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case int32:
		return float64(v), true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return f, err == nil
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func findMapValue(m map[string]any, keys ...string) (any, bool) {
	for _, k := range keys {
		if val, ok := m[k]; ok {
			return val, true
		}
	}
	for _, target := range keys {
		targetLower := strings.ToLower(target)
		for k, val := range m {
			if strings.ToLower(strings.TrimSpace(k)) == targetLower {
				return val, true
			}
		}
	}
	return nil, false
}

func parseCoordinateMap(v map[string]any) (Coordinate, error) {
	latVal, latOk := findMapValue(v, "latitude", "lat")
	lngVal, lngOk := findMapValue(v, "longitude", "lng", "lon", "long")
	if !latOk || !lngOk {
		return Coordinate{}, fmt.Errorf("coordinate must contain 'latitude' (or 'lat') and 'longitude' (or 'lng'/'lon')")
	}
	lat, ok1 := toFloat64(latVal)
	lng, ok2 := toFloat64(lngVal)
	if !ok1 || !ok2 {
		return Coordinate{}, fmt.Errorf("'latitude' and 'longitude' must be numbers")
	}
	return validateCoordinate(Coordinate{Latitude: lat, Longitude: lng})
}

func ParseCoordinate(val any) (Coordinate, error) {
	if val == nil {
		return Coordinate{}, fmt.Errorf("coordinate cannot be nil")
	}
	switch v := val.(type) {
	case Coordinate:
		return validateCoordinate(v)
	case *Coordinate:
		if v == nil {
			return Coordinate{}, fmt.Errorf("coordinate pointer is nil")
		}
		return validateCoordinate(*v)
	case map[string]any:
		return parseCoordinateMap(v)
	case map[string]float64:
		converted := make(map[string]any, len(v))
		for k, val := range v {
			converted[k] = val
		}
		return parseCoordinateMap(converted)
	case map[string]string:
		converted := make(map[string]any, len(v))
		for k, val := range v {
			converted[k] = val
		}
		return parseCoordinateMap(converted)
	case map[any]any:
		converted := make(map[string]any, len(v))
		for k, val := range v {
			converted[fmt.Sprintf("%v", k)] = val
		}
		return parseCoordinateMap(converted)
	default:
		return Coordinate{}, fmt.Errorf("invalid coordinate type: expected object with latitude and longitude")
	}
}

func validateCoordinate(c Coordinate) (Coordinate, error) {
	if c.Latitude < -90.0 || c.Latitude > 90.0 {
		return Coordinate{}, fmt.Errorf("latitude %v out of range [-90.0, 90.0]", c.Latitude)
	}
	if c.Longitude < -180.0 || c.Longitude > 180.0 {
		return Coordinate{}, fmt.Errorf("longitude %v out of range [-180.0, 180.0]", c.Longitude)
	}
	return c, nil
}

func parseBoundingBoxMap(v map[string]any) (BoundingBox, error) {
	minLatVal, ok1 := findMapValue(v, "min_lat", "min_latitude", "minlat")
	maxLatVal, ok2 := findMapValue(v, "max_lat", "max_latitude", "maxlat")
	minLngVal, ok3 := findMapValue(v, "min_lng", "min_longitude", "min_lon", "minlng", "minlon")
	maxLngVal, ok4 := findMapValue(v, "max_lng", "max_longitude", "max_lon", "maxlng", "maxlon")
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return BoundingBox{}, fmt.Errorf("bounding box must contain min_lat, max_lat, min_lng, and max_lng")
	}
	minLat, ok1 := toFloat64(minLatVal)
	maxLat, ok2 := toFloat64(maxLatVal)
	minLng, ok3 := toFloat64(minLngVal)
	maxLng, ok4 := toFloat64(maxLngVal)
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return BoundingBox{}, fmt.Errorf("bounding box coordinates must be numbers")
	}
	return validateBoundingBox(BoundingBox{
		MinLat: minLat,
		MaxLat: maxLat,
		MinLng: minLng,
		MaxLng: maxLng,
	})
}

func ParseBoundingBox(val any) (BoundingBox, error) {
	if val == nil {
		return BoundingBox{}, fmt.Errorf("bounding box cannot be nil")
	}
	switch v := val.(type) {
	case BoundingBox:
		return validateBoundingBox(v)
	case *BoundingBox:
		if v == nil {
			return BoundingBox{}, fmt.Errorf("bounding box pointer is nil")
		}
		return validateBoundingBox(*v)
	case map[string]any:
		return parseBoundingBoxMap(v)
	case map[string]float64:
		converted := make(map[string]any, len(v))
		for k, val := range v {
			converted[k] = val
		}
		return parseBoundingBoxMap(converted)
	case map[string]string:
		converted := make(map[string]any, len(v))
		for k, val := range v {
			converted[k] = val
		}
		return parseBoundingBoxMap(converted)
	case map[any]any:
		converted := make(map[string]any, len(v))
		for k, val := range v {
			converted[fmt.Sprintf("%v", k)] = val
		}
		return parseBoundingBoxMap(converted)
	default:
		return BoundingBox{}, fmt.Errorf("invalid bounding box type")
	}
}

func validateBoundingBox(b BoundingBox) (BoundingBox, error) {
	if b.MinLat < -90.0 || b.MinLat > 90.0 {
		return BoundingBox{}, fmt.Errorf("min_lat %v out of range [-90.0, 90.0]", b.MinLat)
	}
	if b.MaxLat < -90.0 || b.MaxLat > 90.0 {
		return BoundingBox{}, fmt.Errorf("max_lat %v out of range [-90.0, 90.0]", b.MaxLat)
	}
	if b.MinLat > b.MaxLat {
		return BoundingBox{}, fmt.Errorf("min_lat (%v) cannot be greater than max_lat (%v)", b.MinLat, b.MaxLat)
	}
	if b.MinLng < -180.0 || b.MinLng > 180.0 {
		return BoundingBox{}, fmt.Errorf("min_lng %v out of range [-180.0, 180.0]", b.MinLng)
	}
	if b.MaxLng < -180.0 || b.MaxLng > 180.0 {
		return BoundingBox{}, fmt.Errorf("max_lng %v out of range [-180.0, 180.0]", b.MaxLng)
	}
	return b, nil
}
