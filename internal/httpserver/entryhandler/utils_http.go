package entryhandler

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// sanitizeZipEntryName makes a user-provided file name safe for use as a ZIP
// entry name. Path separators are replaced and empty/relative-segment names are
// neutralized, so a crafted file name like "../../evil.sh" can never escape the
// intended directory when the export archive is extracted (zip-slip).
func sanitizeZipEntryName(name string) string {
	name = strings.ReplaceAll(name, "\\", "_")
	name = strings.ReplaceAll(name, "/", "_")
	if name == "" || name == "." || name == ".." {
		return "unnamed"
	}
	return name
}

// jsonString renders s as a JSON string literal without HTML escaping, keeping
// response bytes byte-compatible with plain values (no \u003c-style escapes)
// while still escaping quotes, backslashes and control characters so the value
// can never break out of the surrounding JSON string.
func jsonString(s string) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return "", err
	}
	return strings.TrimSuffix(buf.String(), "\n"), nil
}

// writeJSONFileResponseStream streams a binary io.Reader as a Base64-encoded Data URI JSON response directly to w.
// This eliminates buffering full files in memory.
func writeJSONFileResponseStream(w io.Writer, filename, mimeType string, size uint64, reader io.Reader) error {
	fnJSON, err := jsonString(filename)
	if err != nil {
		return err
	}
	mtJSON, err := jsonString(mimeType)
	if err != nil {
		return err
	}

	// The data URI is rendered as a complete JSON string literal (with its
	// opening quote) minus the trailing quote, so the base64 payload can be
	// streamed directly into the JSON string value afterwards. Rendering the
	// whole prefix through the JSON encoder keeps untrusted MIME types from
	// breaking out of the string literal.
	dataPrefix, err := jsonString(fmt.Sprintf("data:%s;base64,", mimeType))
	if err != nil {
		return err
	}
	dataPrefix = dataPrefix[:len(dataPrefix)-1]

	var prefix string
	if size > 0 {
		prefix = fmt.Sprintf(`{"filename":%s,"mime_type":%s,"size":%d,"data":%s`, fnJSON, mtJSON, size, dataPrefix)
	} else {
		prefix = fmt.Sprintf(`{"filename":%s,"mime_type":%s,"data":%s`, fnJSON, mtJSON, dataPrefix)
	}
	if _, err := io.WriteString(w, prefix); err != nil {
		return err
	}

	b64Writer := base64.NewEncoder(base64.StdEncoding, w)
	if _, err := io.Copy(b64Writer, reader); err != nil {
		b64Writer.Close()
		return err
	}
	if err := b64Writer.Close(); err != nil {
		return err
	}

	if _, err := io.WriteString(w, `"}`); err != nil {
		return err
	}

	return nil
}

// parseRange parses a standard HTTP Range header (e.g. "bytes=1000-2000")
// and returns the offset and length relative to the fileSize.
func parseRange(header string, fileSize int64) ([]byteRange, error) {
	if !strings.HasPrefix(header, "bytes=") {
		return nil, fmt.Errorf("invalid unit")
	}

	var ranges []byteRange
	chk := strings.TrimPrefix(header, "bytes=")
	parts := strings.Split(chk, ",") // Handle multiple ranges "0-50, 100-150"

	for _, part := range parts {
		part = strings.TrimSpace(part)
		bounds := strings.Split(part, "-")
		if len(bounds) != 2 {
			continue
		}

		var start, end int64
		var err error

		if bounds[0] == "" {
			// suffix-byte-range-spec: "-500" (Last 500 bytes)
			suffix, err := strconv.ParseInt(bounds[1], 10, 64)
			if err != nil {
				return nil, err
			}
			if suffix > fileSize {
				suffix = fileSize
			}
			start = fileSize - suffix
			end = fileSize - 1
		} else {
			start, err = strconv.ParseInt(bounds[0], 10, 64)
			if err != nil {
				return nil, err
			}

			if bounds[1] == "" {
				// "100-" (From 100 to end)
				end = fileSize - 1
			} else {
				// "100-200"
				end, err = strconv.ParseInt(bounds[1], 10, 64)
				if err != nil {
					return nil, err
				}
			}
		}

		// Validation
		if start < 0 {
			start = 0
		}
		if end >= fileSize {
			end = fileSize - 1
		}
		if start > end {
			return nil, fmt.Errorf("invalid range")
		}

		ranges = append(ranges, byteRange{
			start:  start,
			length: end - start + 1,
		})
	}
	return ranges, nil
}
