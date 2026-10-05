package shared

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	sizeRegex     = regexp.MustCompile(`(?i)^(\d+)\s*([a-z]*)$`)
	durationRegex = regexp.MustCompile(`(?i)^(\d+)\s*([a-z]+)$`)
)

// ParseSize parses a size string (e.g., "100G", "500MB", "1024 bytes") into bytes.
func ParseSize(sizeStr string) (uint64, error) {
	matches := sizeRegex.FindStringSubmatch(strings.TrimSpace(sizeStr))

	if len(matches) < 3 {
		return 0, fmt.Errorf("invalid size format: %s", sizeStr)
	}

	value, err := strconv.ParseUint(matches[1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size number: %s", matches[1])
	}

	unit := strings.ToUpper(matches[2]) // Normalize to uppercase for the switch

	switch unit {
	case "T", "TB":
		return scaleSize(value, 1<<40, sizeStr)
	case "G", "GB":
		return scaleSize(value, 1<<30, sizeStr)
	case "M", "MB":
		return scaleSize(value, 1<<20, sizeStr)
	case "K", "KB":
		return scaleSize(value, 1<<10, sizeStr)
	case "", "B", "BYTE", "BYTES":
		return value, nil
	default:
		return 0, fmt.Errorf("unsupported size unit: %s", unit)
	}
}

// scaleSize multiplies value by factor and reports an error instead of silently
// wrapping around on overflow.
func scaleSize(value, factor uint64, sizeStr string) (uint64, error) {
	if value > math.MaxUint64/factor {
		return 0, fmt.Errorf("size value out of range: %s", sizeStr)
	}
	return value * factor, nil
}

// ParseDuration parses a duration string with support for days and various aliases
// (e.g., "30d", "24 hours", "15 mins").
func ParseDuration(durationStr string) (time.Duration, error) {
	trimmedStr := strings.TrimSpace(durationStr)

	// Handle "0" as a special case for "disabled"
	if trimmedStr == "0" {
		return 0, nil
	}

	// Capture the number and any trailing alphabetical characters
	matches := durationRegex.FindStringSubmatch(trimmedStr)

	if len(matches) < 3 {
		return 0, fmt.Errorf("invalid duration format: %s", durationStr)
	}

	value, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0, fmt.Errorf("invalid duration number: %s", matches[1])
	}

	// If value is 0 (e.g., "0d"), return 0 duration
	if value == 0 {
		return 0, nil
	}

	unit := strings.ToLower(matches[2]) // Normalize to lowercase for the switch
	switch unit {
	case "d", "day", "days":
		return scaleDuration(value, 24*time.Hour, durationStr)
	case "h", "hr", "hrs", "hour", "hours":
		return scaleDuration(value, time.Hour, durationStr)
	case "m", "min", "mins", "minute", "minutes":
		return scaleDuration(value, time.Minute, durationStr)
	case "s", "sec", "secs", "second", "seconds":
		return scaleDuration(value, time.Second, durationStr)
	default:
		return 0, fmt.Errorf("unsupported duration unit: %s", unit)
	}
}

// scaleDuration multiplies value by unit and reports an error instead of silently
// wrapping around on overflow.
func scaleDuration(value int, unit time.Duration, durationStr string) (time.Duration, error) {
	if value > int(math.MaxInt64/int64(unit)) {
		return 0, fmt.Errorf("duration value out of range: %s", durationStr)
	}
	return time.Duration(value) * unit, nil
}
