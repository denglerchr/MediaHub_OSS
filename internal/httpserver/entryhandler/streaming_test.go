package entryhandler

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestWriteJSONFileResponseStream(t *testing.T) {
	testCases := []struct {
		name     string
		filename string
		mimeType string
		dataSize int
	}{
		{
			name:     "Empty file",
			filename: "empty.txt",
			mimeType: "text/plain",
			dataSize: 0,
		},
		{
			name:     "Small image",
			filename: "photo.jpg",
			mimeType: "image/jpeg",
			dataSize: 512,
		},
		{
			name:     "Medium WebP preview",
			filename: "preview.webp",
			mimeType: "image/webp",
			dataSize: 64 * 1024,
		},
		{
			name:     "Large 1MB file",
			filename: "large_document.pdf",
			mimeType: "application/pdf",
			dataSize: 1024 * 1024,
		},
		{
			name:     "Filename with quotes and special characters",
			filename: `my "cool" file & (1).png`,
			mimeType: "image/png",
			dataSize: 1024,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			rawBytes := make([]byte, tc.dataSize)
			if tc.dataSize > 0 {
				_, err := rand.Read(rawBytes)
				if err != nil {
					t.Fatalf("failed to generate random bytes: %v", err)
				}
			}

			reader := bytes.NewReader(rawBytes)
			var out bytes.Buffer

			sizeUint := uint64(tc.dataSize)
			err := writeJSONFileResponseStream(&out, tc.filename, tc.mimeType, sizeUint, reader)
			if err != nil {
				t.Fatalf("writeJSONFileResponseStream returned error: %v", err)
			}

			var resp FileJSONResponse
			if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
				t.Fatalf("failed to parse output as FileJSONResponse JSON: %v, raw output prefix: %s", err, string(out.Bytes()[:min(len(out.Bytes()), 200)]))
			}

			if resp.Filename != tc.filename {
				t.Errorf("expected filename %q, got %q", tc.filename, resp.Filename)
			}
			if resp.MimeType != tc.mimeType {
				t.Errorf("expected mimeType %q, got %q", tc.mimeType, resp.MimeType)
			}
			if resp.Size != sizeUint {
				t.Errorf("expected size %d, got %d", sizeUint, resp.Size)
			}

			expectedPrefix := fmt.Sprintf("data:%s;base64,", tc.mimeType)
			if !strings.HasPrefix(resp.Data, expectedPrefix) {
				t.Fatalf("expected data URI prefix %q, got: %s", expectedPrefix, resp.Data[:min(len(resp.Data), len(expectedPrefix)+10)])
			}

			b64Payload := strings.TrimPrefix(resp.Data, expectedPrefix)
			decoded, err := base64.StdEncoding.DecodeString(b64Payload)
			if err != nil {
				t.Fatalf("failed to decode base64 data: %v", err)
			}

			if !bytes.Equal(decoded, rawBytes) {
				t.Errorf("decoded data does not match original (lengths: decoded=%d, original=%d)", len(decoded), len(rawBytes))
			}
		})
	}

	t.Run("Omitted size (0)", func(t *testing.T) {
		rawBytes := []byte("hello world")
		reader := bytes.NewReader(rawBytes)
		var out bytes.Buffer

		err := writeJSONFileResponseStream(&out, "test.txt", "text/plain", 0, reader)
		if err != nil {
			t.Fatalf("writeJSONFileResponseStream returned error: %v", err)
		}

		if strings.Contains(out.String(), `"size":`) {
			t.Errorf("expected output to omit size field, got: %s", out.String())
		}

		var resp FileJSONResponse
		if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
			t.Fatalf("failed to parse output: %v", err)
		}

		if resp.Filename != "test.txt" || resp.MimeType != "text/plain" || resp.Size != 0 {
			t.Errorf("unexpected parsed response: %+v", resp)
		}
	})

	t.Run("Malicious MIME type injection escaping", func(t *testing.T) {
		rawBytes := []byte("payload")
		reader := bytes.NewReader(rawBytes)
		var out bytes.Buffer

		maliciousMime := `x";"injected":"true`
		err := writeJSONFileResponseStream(&out, "test.bin", maliciousMime, uint64(len(rawBytes)), reader)
		if err != nil {
			t.Fatalf("writeJSONFileResponseStream failed: %v", err)
		}

		var resp FileJSONResponse
		if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
			t.Fatalf("output is not valid JSON: %v, raw: %s", err, out.String())
		}
		if resp.MimeType != maliciousMime {
			t.Errorf("expected MIME %q, got %q", maliciousMime, resp.MimeType)
		}
		expectedDataPrefix := fmt.Sprintf("data:%s;base64,", maliciousMime)
		if !strings.HasPrefix(resp.Data, expectedDataPrefix) {
			t.Errorf("expected data to start with %q, got %q", expectedDataPrefix, resp.Data)
		}
	})
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
