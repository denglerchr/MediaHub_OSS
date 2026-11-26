package repository

import "strings"

// EntryStatus represents the status of a database media entry.
type EntryStatus uint8

// Status constants used for the Entry
const (
	EntryStatusReady      EntryStatus = 0x00
	EntryStatusProcessing EntryStatus = 0x01
	EntryStatusError      EntryStatus = 0x02
	EntryStatusDeleting   EntryStatus = 0x03
	EntryStatusQueued     EntryStatus = 0x04
)

// ParseEntryStatus parses a status name (trimmed, case-insensitive) into an EntryStatus.
func ParseEntryStatus(s string) (EntryStatus, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "ready":
		return EntryStatusReady, true
	case "processing":
		return EntryStatusProcessing, true
	case "error":
		return EntryStatusError, true
	case "deleting":
		return EntryStatusDeleting, true
	case "queued":
		return EntryStatusQueued, true
	default:
		return 0, false
	}
}

// GetAllEntryStatuses provides a centralized list of all valid statuses.
func GetAllEntryStatuses() []EntryStatus {
	return []EntryStatus{
		EntryStatusReady,
		EntryStatusProcessing,
		EntryStatusError,
		EntryStatusDeleting,
		EntryStatusQueued,
	}
}

func GetEntryStatusString(status EntryStatus) string {
	switch status {
	case EntryStatusReady:
		return "ready"
	case EntryStatusProcessing:
		return "processing"
	case EntryStatusError:
		return "error"
	case EntryStatusDeleting:
		return "deleting"
	case EntryStatusQueued:
		return "queued"
	default:
		return "unknown"
	}
}
