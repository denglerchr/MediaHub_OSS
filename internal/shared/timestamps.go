package shared

import "math"

// MissingTimestamp is the sentinel value representing an omitted or unset
// int64 Unix millisecond timestamp. Negative values (< 1970-01-01T00:00:00Z)
// and 0 (1970-01-01T00:00:00Z) are valid historical timestamps; math.MinInt64
// is reserved as the missing-value sentinel instead.
const MissingTimestamp int64 = math.MinInt64
