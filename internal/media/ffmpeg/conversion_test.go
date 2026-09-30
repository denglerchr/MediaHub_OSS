package ffmpeg

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"mediahub_oss/internal/media"
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
			actual, err := BuildImageFilter(tc.width, tc.height, tc.fit)
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
	filter, err := BuildImageFilter(800, 480, "  PAD-WHITE  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(filter, "color=white") {
		t.Errorf("expected white padding, got: %s", filter)
	}
}

func TestCapabilities_RecommendedVsAllSupported(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	c, err := NewFFMPEGConverter("", "", logger)
	if err != nil {
		t.Fatalf("NewFFMPEGConverter failed: %v", err)
	}
	defer c.Shutdown(context.Background())
	// Ensure ffmpegPath is non-empty for unit testing even on machines without ffmpeg in PATH
	c.ffmpegPath = "ffmpeg"
	c.initConversions()

	// GetOutputMimeTypes should only return IsRecommended profiles
	imageOutputs := c.GetOutputMimeTypes("image")
	for _, rec := range []string{"image/jpeg", "image/webp", "image/avif"} {
		if !slices.Contains(imageOutputs, rec) {
			t.Errorf("expected recommended image output %q in %v", rec, imageOutputs)
		}
	}
	for _, nonRec := range []string{"image/png", "image/gif"} {
		if slices.Contains(imageOutputs, nonRec) {
			t.Errorf("did not expect non-recommended image output %q in %v", nonRec, imageOutputs)
		}
	}

	audioOutputs := c.GetOutputMimeTypes("audio")
	for _, rec := range []string{"audio/flac", "audio/opus"} {
		if !slices.Contains(audioOutputs, rec) {
			t.Errorf("expected recommended audio output %q in %v", rec, audioOutputs)
		}
	}
	for _, nonRec := range []string{"audio/mpeg", "audio/mp3", "audio/wav", "audio/ogg"} {
		if slices.Contains(audioOutputs, nonRec) {
			t.Errorf("did not expect non-recommended audio output %q in %v", nonRec, audioOutputs)
		}
	}

	// CanConvert should allow all supported profiles (both recommended and non-recommended)
	allTargets := []struct {
		input  string
		target string
		width  int
	}{
		{"image/jpeg", "image/png", 0},
		{"image/png", "image/png", 200},
		{"image/webp", "image/gif", 0},
		{"audio/flac", "audio/mpeg", 0},
		{"audio/flac", "audio/mp3", 0},
		{"audio/flac", "mp3", 0},
		{"audio/flac", "audio/wav", 0},
		{"audio/flac", "audio/ogg", 0},
	}
	for _, tc := range allTargets {
		check := c.CanConvert(tc.input, media.ConversionOptions{
			TargetMimeType: tc.target,
			Width:          tc.width,
		})
		if !check.CanConvert || !check.NeedsConversion {
			t.Errorf("CanConvert(%q -> %q, width=%d) = %+v, expected CanConvert=true, NeedsConversion=true",
				tc.input, tc.target, tc.width, check)
		}
	}
}

func TestFFprobeSiblingPathResolution(t *testing.T) {
	// Ensure directory names containing "ffmpeg" are not corrupted when deriving ffprobe path
	tmpDir := t.TempDir()
	ffmpegDir := filepath.Join(tmpDir, "ffmpeg-7.0", "bin")
	if err := os.MkdirAll(ffmpegDir, 0755); err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	fakeFFmpeg := filepath.Join(ffmpegDir, "ffmpeg.exe")
	fakeFFprobe := filepath.Join(ffmpegDir, "ffprobe.exe")
	if err := os.WriteFile(fakeFFmpeg, []byte("fake"), 0755); err != nil {
		t.Fatalf("failed to write fake ffmpeg: %v", err)
	}
	if err := os.WriteFile(fakeFFprobe, []byte("fake"), 0755); err != nil {
		t.Fatalf("failed to write fake ffprobe: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	conv, err := NewFFMPEGConverter(fakeFFmpeg, "", logger)
	if err != nil {
		t.Fatalf("NewFFMPEGConverter failed: %v", err)
	}
	defer conv.Shutdown(context.Background())

	probePath, err := conv.GetFFprobePath()
	if err != nil {
		t.Fatalf("expected ffprobe to be found at %q, got error: %v", fakeFFprobe, err)
	}
	if probePath != fakeFFprobe {
		t.Errorf("GetFFprobePath() = %q, expected %q", probePath, fakeFFprobe)
	}
}

func TestExtractFields_EdgeCases(t *testing.T) {
	probe := ffprobeOutput{}
	probe.Streams = append(probe.Streams, struct {
		CodecType string `json:"codec_type"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
		Channels  int    `json:"channels"`
		Duration  string `json:"duration"`
	}{
		CodecType: "audio",
		Channels:  300, // exceeds MaxUint8
		Duration:  "12.5",
	})
	probe.Format.Duration = "-0.001" // non-positive format duration should not override valid stream duration

	fields, err := extractFields(probe, "audio")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fields["channels"] != uint8(math.MaxUint8) {
		t.Errorf("expected channels clamped to %d, got %v", math.MaxUint8, fields["channels"])
	}
	if fields["duration"] != 12.5 {
		t.Errorf("expected duration 12.5, got %v", fields["duration"])
	}
}

type nonConcurrentReaderAt struct {
	*bytes.Reader
}

func TestLocalStreamServer_Register(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, err := NewLocalStreamServer(logger)
	if err != nil {
		t.Fatalf("failed to start LocalStreamServer: %v", err)
	}
	defer srv.Shutdown(context.Background())

	// Custom type implementing io.ReaderAt that is NOT in the safe allowlist should be buffered into *bytes.Reader
	customStream := &nonConcurrentReaderAt{Reader: bytes.NewReader([]byte("hello world"))}
	id, _, err := srv.Register(customStream, time.Minute)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	defer srv.Unregister(id)

	srv.mu.RLock()
	session := srv.sessions[id]
	srv.mu.RUnlock()

	if _, ok := session.readerAt.(*bytes.Reader); !ok {
		t.Errorf("expected non-allowlisted ReaderAt stream to be buffered into *bytes.Reader, got %T", session.readerAt)
	}
}

