package customerrors

type Error string

// Implement the error interface
func (e Error) Error() string { return string(e) }

//------------
// Definitions
//------------

// cli errors
const (

	// File errors
	ErrStorageUnavailable = Error("could not connect to the file storage")
	ErrorCreateFile       = Error("could not create the file")
	ErrorEncodeFile       = Error("could not encode to file")

	// Repository errors
	ErrRepoUnavailable = Error("could not connect to the repository")
	ErrUserExists      = Error("user already exists")
	ErrInvalidName     = Error("invalid name")
	ErrDatabaseExists  = Error("database already exists")

	// Media errors
	ErrUnsupportedMedia = Error("unsupported media type")
	ErrBadMimeType      = Error("mime type not matching content type")

	// Import errors
	ErrUnmappedFieldAbort = Error("unmapped field encountered, aborting import")

	// Distributed Lock errors
	ErrLockNotAcquired = Error("lock not acquired")
	ErrLockNotReleased = Error("lock not released")

	// Generic errors
	ErrPermissionDenied = Error("permission denied")
	ErrNotFound         = Error("not found")
	ErrUnavailable      = Error("service unavailable")
	ErrValidation       = Error("validation error")
	ErrNotImplemented   = Error("not implemented")
	ErrConflict         = Error("conflict")

	// Processing errors
	ErrResourceExhausted = Error("concurrency limit reached")
)
