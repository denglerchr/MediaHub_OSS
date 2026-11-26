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
	ErrorCreateFile       = Error("could not create the file")
	ErrorEncodeFile       = Error("could not encode to file")

	// Repository errors
	ErrUserExists      = Error("user already exists")
	ErrInvalidName     = Error("invalid name")
	ErrDatabaseExists  = Error("database already exists")

	// Media errors
	ErrBadMimeType      = Error("mime type not matching content type")

	// Import errors
	ErrUnmappedFieldAbort = Error("unmapped field encountered, aborting import")

	// Distributed Lock errors
	ErrLockNotAcquired = Error("lock not acquired")

	// Generic errors
	ErrPermissionDenied = Error("permission denied")
	ErrNotFound         = Error("not found")
	ErrUnavailable      = Error("service unavailable")
	ErrValidation       = Error("validation error")
	ErrConflict         = Error("conflict")

	// Processing errors
	ErrResourceExhausted = Error("concurrency limit reached")
)
