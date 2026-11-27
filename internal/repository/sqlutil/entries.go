package sqlutil

import "math"

// SignedSizeDelta returns newSize - oldSize clamped to the int64 range for database statistics updates.
func SignedSizeDelta(newSize, oldSize uint64) int64 {
	if newSize >= oldSize {
		diff := newSize - oldSize
		if diff > math.MaxInt64 {
			return math.MaxInt64
		}
		return int64(diff)
	}
	diff := oldSize - newSize
	if diff > math.MaxInt64 {
		return -math.MaxInt64
	}
	return -int64(diff)
}
