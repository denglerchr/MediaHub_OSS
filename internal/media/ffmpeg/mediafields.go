package ffmpeg

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os/exec"
	"strconv"
	"time"

	"mediahub_oss/internal/media"
)

// ffprobeOutput maps the structure of the JSON returned by ffprobe.
type ffprobeOutput struct {
	Streams []struct {
		CodecType string `json:"codec_type"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
		Channels  int    `json:"channels"`
		Duration  string `json:"duration"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

// ReadMediaFieldsFromFile extracts metadata by reading the file directly from the disk.
func (c *FfmpegConverter) ReadMediaFieldsFromFile(ctx context.Context, filepath string, contentType string) (map[string]any, error) {
	return c.runFFprobe(ctx, filepath, contentType)
}

// ReadMediaFieldsFromStream extracts metadata purely in-memory by exposing the stream
// via the internal HTTP loopback server.
func (c *FfmpegConverter) ReadMediaFieldsFromStream(ctx context.Context, inputData io.ReadSeeker, contentType string) (map[string]any, error) {
	if path, ok := localFilePath(inputData); ok {
		return c.runFFprobe(ctx, path, contentType)
	}

	id, fullURL, err := c.localServer.Register(inputData, 2*time.Minute)
	if err != nil {
		return map[string]any{}, fmt.Errorf("failed to register stream: %w", err)
	}
	defer c.localServer.Unregister(id)

	return c.runFFprobe(ctx, fullURL, contentType)
}

// runFFprobe contains the core execution logic shared by both file and stream inputs.
func (c *FfmpegConverter) runFFprobe(ctx context.Context, inputSource string, contentType string) (map[string]any, error) {
	probePath, err := c.GetFFprobePath()
	if err != nil {
		return nil, fmt.Errorf("ffprobe is not available: %w", err)
	}

	args := []string{
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		"-i", inputSource,
	}

	// Bind the ffprobe process to the provided context
	cmd := exec.CommandContext(ctx, probePath, args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		c.logger.Error("ffprobe extraction failed", "error", err, "stderr", stderr.String(), "source", inputSource)
		return nil, fmt.Errorf("ffprobe error: %w", err)
	}

	// Parse the ffprobe JSON output
	var probe ffprobeOutput
	if err := json.Unmarshal(stdout.Bytes(), &probe); err != nil {
		return nil, fmt.Errorf("failed to parse ffprobe json: %w", err)
	}

	return extractFields(probe, contentType)
}

// extractFields parses the JSON output and maps it to the expected interface based directly on the DB content type.
func extractFields(probe ffprobeOutput, contentType string) (map[string]any, error) {

	fields := make(map[string]any)
	var width, height uint64
	var duration float64
	var channels uint8

	for _, s := range probe.Streams {
		if s.CodecType == "video" {
			if s.Width > 0 && width == 0 {
				width = uint64(s.Width)
			}
			if s.Height > 0 && height == 0 {
				height = uint64(s.Height)
			}
			if s.Duration != "" {
				if d, err := strconv.ParseFloat(s.Duration, 64); err == nil && d > 0 && duration == 0 {
					duration = d
				}
			}
		}
		if s.CodecType == "audio" {
			if s.Channels > 0 && channels == 0 {
				if s.Channels > math.MaxUint8 {
					channels = math.MaxUint8
				} else {
					channels = uint8(s.Channels)
				}
			}
			if s.Duration != "" {
				if d, err := strconv.ParseFloat(s.Duration, 64); err == nil && d > 0 && duration == 0 {
					duration = d
				}
			}
		}
	}
	if probe.Format.Duration != "" {
		if d, err := strconv.ParseFloat(probe.Format.Duration, 64); err == nil && d > 0 {
			duration = d
		}
	}

	// BYPASS MIME TYPE LOOKUP - USE CONTENT TYPE DIRECTLY
	expectedFields, err := media.GetMetadataFields(contentType)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve expected metadata fields for %s: %w", contentType, err)
	}

	// Populate the map only with the fields the database schema expects
	for _, field := range expectedFields {
		switch field.Name {
		case "width":
			fields[field.Name] = width
		case "height":
			fields[field.Name] = height
		case "duration":
			fields[field.Name] = duration
		case "channels":
			fields[field.Name] = channels
		}
	}

	return fields, nil
}
