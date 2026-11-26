package ulid

import (
	"encoding/json"
	"errors"
	"mediahub_oss/internal/shared/customerrors"
	"testing"
)

func TestNewAndZeroValue(t *testing.T) {
	u1 := New()
	u2 := New()
	if u1.IsZero() || u2.IsZero() {
		t.Fatal("expected New() to return non-zero ULID")
	}
	if u1 == u2 {
		t.Fatalf("expected unique ULIDs from consecutive New() calls, got %s", u1)
	}
	if !IsValid(u1.String()) {
		t.Fatalf("expected New().String() to be valid, got %q", u1.String())
	}

	var zero ULID
	if !zero.IsZero() {
		t.Fatal("expected zero ULID to report IsZero() == true")
	}
	if got, want := zero.String(), "00000000000000000000000000"; got != want {
		t.Fatalf("expected zero ULID String() == %q, got %q", want, got)
	}
}

func TestParseAndIsValid(t *testing.T) {
	validStr := "01HGFB9Z5W7ABCDEFGHJKMNPQR"
	parsed, err := Parse(validStr)
	if err != nil {
		t.Fatalf("expected %q to parse cleanly, got %v", validStr, err)
	}
	if parsed.String() != validStr {
		t.Fatalf("expected parsed.String() == %q, got %q", validStr, parsed.String())
	}
	if !IsValid(validStr) {
		t.Fatalf("expected IsValid(%q) == true", validStr)
	}
	if MustParse(validStr) != parsed {
		t.Fatal("expected MustParse to match Parse")
	}

	invalidCases := []string{
		"",
		".",
		"..",
		"../escape",
		"previews",
		"00000000000000000000000000", // all-zero ULID rejected
		"01HGFB9Z5W7ABCDEFGHJKMNPQ",  // too short (25 chars)
		"01HGFB9Z5W7ABCDEFGHJKMNPQRS", // too long (27 chars)
		"01HGFB9Z5W7ABCDEFGHIJKMNPQ", // contains 'I'
		"01HGFB9Z5W7ABCDEFGHJLKMNPQ", // contains 'L'
		"01HGFB9Z5W7ABCDEFGHJOKMNPQ", // contains 'O'
		"01HGFB9Z5W7ABCDEFGHJUKMNPQ", // contains 'U'
	}
	for _, tc := range invalidCases {
		_, err := Parse(tc)
		if !errors.Is(err, customerrors.ErrValidation) {
			t.Errorf("Parse(%q): expected ErrValidation, got %v", tc, err)
		}
		if IsValid(tc) {
			t.Errorf("IsValid(%q): expected false", tc)
		}
	}
}

func TestJSONMarshaling(t *testing.T) {
	type payload struct {
		ID     ULID         `json:"id"`
		Counts map[ULID]int `json:"counts"`
	}

	id1 := MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR")
	id2 := MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQS")

	in := payload{
		ID: id1,
		Counts: map[ULID]int{
			id1: 3,
			id2: 7,
		},
	}

	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var out payload
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}
	if out.ID != in.ID || out.Counts[id1] != 3 || out.Counts[id2] != 7 {
		t.Fatalf("JSON round-trip mismatch: got %+v, want %+v", out, in)
	}

	var bad payload
	if err := json.Unmarshal([]byte(`{"id":"../bad"}`), &bad); !errors.Is(err, customerrors.ErrValidation) {
		t.Fatalf("expected ErrValidation unmarshaling invalid ULID, got %v", err)
	}
}

func TestSQLValueAndScan(t *testing.T) {
	u := MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR")

	val, err := u.Value()
	if err != nil {
		t.Fatalf("Value() failed: %v", err)
	}
	strVal, ok := val.(string)
	if !ok || strVal != u.String() {
		t.Fatalf("expected driver.Value string %q, got %#v", u.String(), val)
	}

	var zero ULID
	if _, err := zero.Value(); !errors.Is(err, customerrors.ErrValidation) {
		t.Fatalf("expected ErrValidation from zero ULID.Value(), got %v", err)
	}

	var fromString ULID
	if err := fromString.Scan(strVal); err != nil {
		t.Fatalf("Scan(string) failed: %v", err)
	}
	if fromString != u {
		t.Fatalf("Scan(string) mismatch: got %s, want %s", fromString, u)
	}

	var fromBytes ULID
	if err := fromBytes.Scan([]byte(strVal)); err != nil {
		t.Fatalf("Scan([]byte) failed: %v", err)
	}
	if fromBytes != u {
		t.Fatalf("Scan([]byte) mismatch: got %s, want %s", fromBytes, u)
	}

	for _, bad := range []any{"invalid", []byte("00000000000000000000000000"), 123, nil} {
		var dst ULID
		if err := dst.Scan(bad); !errors.Is(err, customerrors.ErrValidation) {
			t.Errorf("Scan(%#v): expected ErrValidation, got %v", bad, err)
		}
	}
}
