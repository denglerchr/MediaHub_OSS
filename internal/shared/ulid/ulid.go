package ulid

import (
	"database/sql/driver"
	"fmt"
	"mediahub_oss/internal/shared/customerrors"

	oklogulid "github.com/oklog/ulid/v2"
)

// ULID is a validated 128-bit Universally Unique Lexicographically Sortable Identifier.
// Backed by a [16]byte value (comparable and usable as a map key with zero heap allocations),
// its String() output is always 26 uppercase Crockford Base32 characters ([0-9A-HJKMNP-TV-Z]{26})
// and can never contain path separators, '..', SQL metacharacters, or reserved directory names
// like "previews". The zero value (ULID{}) represents an unset identifier (IsZero() == true).
type ULID struct {
	raw oklogulid.ULID
}

// New generates a new cryptographically seeded, lexicographically sortable ULID.
func New() ULID {
	return ULID{raw: oklogulid.Make()}
}

// Parse validates s strictly as a non-zero Crockford Base32 ULID.
func Parse(s string) (ULID, error) {
	parsed, err := oklogulid.ParseStrict(s)
	if err != nil {
		return ULID{}, fmt.Errorf("%w: invalid ulid: %v", customerrors.ErrValidation, err)
	}
	if parsed == (oklogulid.ULID{}) {
		return ULID{}, fmt.Errorf("%w: zero ulid is not allowed", customerrors.ErrValidation)
	}
	return ULID{raw: parsed}, nil
}

// MustParse parses s as a non-zero ULID and panics on error. Intended for static test fixtures.
func MustParse(s string) ULID {
	u, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}

// IsValid reports whether s parses as a valid, non-zero ULID.
func IsValid(s string) bool {
	_, err := Parse(s)
	return err == nil
}

// IsZero reports whether u is the unset zero-value ULID.
func (u ULID) IsZero() bool {
	return u.raw == (oklogulid.ULID{})
}

// String returns the canonical 26-character uppercase Crockford Base32 representation.
func (u ULID) String() string {
	return u.raw.String()
}

// MarshalText implements encoding.TextMarshaler.
func (u ULID) MarshalText() ([]byte, error) {
	return []byte(u.raw.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (u *ULID) UnmarshalText(text []byte) error {
	parsed, err := Parse(string(text))
	if err != nil {
		return err
	}
	*u = parsed
	return nil
}

// Value implements database/sql/driver.Valuer.
func (u ULID) Value() (driver.Value, error) {
	if u.IsZero() {
		return nil, fmt.Errorf("%w: zero ulid", customerrors.ErrValidation)
	}
	return u.raw.String(), nil
}

// Scan implements database/sql.Scanner.
func (u *ULID) Scan(src any) error {
	if u == nil {
		return fmt.Errorf("%w: cannot scan into nil ulid pointer", customerrors.ErrValidation)
	}
	switch v := src.(type) {
	case string:
		parsed, err := Parse(v)
		if err != nil {
			return err
		}
		*u = parsed
		return nil
	case []byte:
		parsed, err := Parse(string(v))
		if err != nil {
			return err
		}
		*u = parsed
		return nil
	default:
		return fmt.Errorf("%w: unsupported scan type %T for ulid", customerrors.ErrValidation, src)
	}
}
