package media_test

import (
	"mediahub_oss/internal/media"
	"testing"
)

func TestNormalizeMimeType(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		// Images
		{"webp", "image/webp"},
		{"image/webp", "image/webp"},
		{".webp", "image/webp"},
		{"jpg", "image/jpeg"},
		{"jpeg", "image/jpeg"},
		{"image/jpeg", "image/jpeg"},
		{"image/jpg", "image/jpeg"},
		{"png", "image/png"},
		{"image/png", "image/png"},
		{"gif", "image/gif"},
		{"image/gif", "image/gif"},
		{"avif", "image/avif"},
		{"image/avif", "image/avif"},
		{"  IMAGE/WEBP  ", "image/webp"},
		{".PNG", "image/png"},

		// Audio
		{"mp3", "audio/mpeg"},
		{"audio/mp3", "audio/mpeg"},
		{"audio/mpeg", "audio/mpeg"},
		{"wav", "audio/wav"},
		{"wave", "audio/wav"},
		{"audio/wav", "audio/wav"},
		{"audio/wave", "audio/wav"},
		{"audio/x-wav", "audio/wav"},
		{"flac", "audio/flac"},
		{"audio/flac", "audio/flac"},
		{"audio/x-flac", "audio/flac"},
		{"opus", "audio/opus"},
		{"audio/opus", "audio/opus"},
		{"ogg", "audio/ogg"},
		{"oga", "audio/ogg"},
		{"audio/ogg", "audio/ogg"},
		{"audio/x-ogg", "audio/ogg"},
		{"application/ogg", "audio/ogg"},
		{"m4a", "audio/mp4"},
		{"audio/m4a", "audio/mp4"},

		// Video
		{"mp4", "video/mp4"},
		{"video/mp4", "video/mp4"},
		{"webm", "video/webm"},
		{"video/webm", "video/webm"},
		{"ogv", "video/ogg"},
		{"video/ogg", "video/ogg"},
		{"mkv", "video/x-matroska"},
		{"avi", "video/x-msvideo"},
		{"flv", "video/x-flv"},
		{"mov", "video/quicktime"},

		// Other
		{"application/pdf", "application/pdf"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			actual := media.NormalizeMimeType(tc.input)
			if actual != tc.expected {
				t.Errorf("NormalizeMimeType(%q) = %q, expected %q", tc.input, actual, tc.expected)
			}
		})
	}
}

func TestIsMimeOfType_AudioOgg(t *testing.T) {
	for _, oggFormat := range []string{"ogg", "audio/ogg", "oga", "audio/x-ogg", "application/ogg", "mp3", "audio/mp3", "m4a", "audio/m4a"} {
		ok, err := media.IsMimeOfType("audio", oggFormat)
		if err != nil {
			t.Fatalf("unexpected error for %q: %v", oggFormat, err)
		}
		if !ok {
			t.Errorf("expected IsMimeOfType('audio', %q) to be true", oggFormat)
		}
	}
}

func TestGetContentType(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"image/png", "image"},
		{"png", "image"},
		{"audio/mpeg", "audio"},
		{"mp3", "audio"},
		{"video/mp4", "video"},
		{"application/pdf", "file"},
		{"unknown/type", "file"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			actual := media.GetContentType(tc.input)
			if actual != tc.expected {
				t.Errorf("GetContentType(%q) = %q, expected %q", tc.input, actual, tc.expected)
			}
		})
	}
}
