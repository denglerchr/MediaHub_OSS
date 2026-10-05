package utils

import (
	"fmt"
	"net/http"
	"strconv"
)

// ParseQueryInt safely parses an integer from query parameters, falling back to a default value if omitted.
func ParseQueryInt(r *http.Request, key string, defaultValue int) (int, error) {
	valStr := r.URL.Query().Get(key)
	if valStr == "" {
		return defaultValue, nil
	}
	val, err := strconv.Atoi(valStr)
	if err != nil {
		return 0, fmt.Errorf("invalid value for parameter '%s': must be an integer", key)
	}
	return val, nil
}

// ParseQueryInt64 safely parses a 64-bit integer from query parameters, falling back to a default value if omitted.
func ParseQueryInt64(r *http.Request, key string, defaultValue int64) (int64, error) {
	valStr := r.URL.Query().Get(key)
	if valStr == "" {
		return defaultValue, nil
	}
	val, err := strconv.ParseInt(valStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid value for parameter '%s': must be an integer", key)
	}
	return val, nil
}
