package ffmpeg_test

import (
	"mediahub_oss/internal/media/ffmpeg"
	"strings"
	"testing"
)

func TestBuildImageFilter(t *testing.T) {
	tests := []struct {
		name        string
		width       int
		height      int
		fit         string
		expected    string
		expectError bool
	}{
		{
			name:     "both dimensions with cut",
			width:    800,
			height:   480,
			fit:      "cut",
			expected: "scale=w=800:h=480:force_original_aspect_ratio=increase,crop=800:480",
		},
		{
			name:     "both dimensions with default fit (empty)",
			width:    800,
			height:   480,
			fit:      "",
			expected: "scale=w=800:h=480:force_original_aspect_ratio=increase,crop=800:480",
		},
		{
			name:     "both dimensions with stretch",
			width:    800,
			height:   480,
			fit:      "stretch",
			expected: "scale=w=800:h=480",
		},
		{
			name:     "both dimensions with pad-white",
			width:    800,
			height:   480,
			fit:      "pad-white",
			expected: "scale=w=800:h=480:force_original_aspect_ratio=decrease,pad=w=800:h=480:x=(ow-iw)/2:y=(oh-ih)/2:color=white",
		},
		{
			name:     "both dimensions with pad-black",
			width:    800,
			height:   480,
			fit:      "pad-black",
			expected: "scale=w=800:h=480:force_original_aspect_ratio=decrease,pad=w=800:h=480:x=(ow-iw)/2:y=(oh-ih)/2:color=black",
		},
		{
			name:     "single dimension width only",
			width:    800,
			height:   0,
			fit:      "cut",
			expected: "scale=w=800:h=-1",
		},
		{
			name:     "single dimension height only",
			width:    0,
			height:   480,
			fit:      "stretch",
			expected: "scale=w=-1:h=480",
		},
		{
			name:     "no dimensions specified",
			width:    0,
			height:   0,
			fit:      "cut",
			expected: "",
		},
		{
			name:        "negative width",
			width:       -10,
			height:      480,
			fit:         "cut",
			expectError: true,
		},
		{
			name:        "negative height",
			width:       800,
			height:      -5,
			fit:         "cut",
			expectError: true,
		},
		{
			name:        "invalid fit mode",
			width:       800,
			height:      480,
			fit:         "invalid-mode",
			expectError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actual, err := ffmpeg.BuildImageFilter(tc.width, tc.height, tc.fit)
			if tc.expectError {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if actual != tc.expected {
				t.Errorf("BuildImageFilter() = %q, expected %q", actual, tc.expected)
			}
		})
	}
}

func TestBuildImageFilter_CaseInsensitive(t *testing.T) {
	filter, err := ffmpeg.BuildImageFilter(800, 480, "  PAD-WHITE  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(filter, "color=white") {
		t.Errorf("expected white padding, got: %s", filter)
	}
}
