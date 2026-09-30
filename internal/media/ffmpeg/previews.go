package ffmpeg

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"time"

	"mediahub_oss/internal/media"
)

const maxPreviewHeight = 200
const maxPreviewWidth = 200

// CreatePreviewFromFile generates a WebP preview directly from a file on disk.
// This is heavily optimized for large files and ensures WebM/MP4 index seeking works natively.
func (c *FfmpegConverter) CreatePreviewFromFile(ctx context.Context, filepath string, outputWriter io.Writer, inputMimeType string) error {
	return c.generatePreview(ctx, filepath, outputWriter, inputMimeType)
}

// CreatePreviewFromStream generates a WebP preview purely in-memory using the LocalStreamServer.
// It bypasses physical disk writes while retaining the ability for FFmpeg to safely seek the stream.
func (c *FfmpegConverter) CreatePreviewFromStream(ctx context.Context, inputData io.ReadSeeker, outputWriter io.Writer, inputMimeType string) error {
	if path, ok := localFilePath(inputData); ok {
		return c.generatePreview(ctx, path, outputWriter, inputMimeType)
	}

	// Register the stream with the local loopback server with a short Time-To-Live.
	id, fullURL, err := c.localServer.Register(inputData, 2*time.Minute)
	if err != nil {
		return fmt.Errorf("failed to register stream: %w", err)
	}

	// Ensure the stream is unregistered from RAM as soon as the preview is done.
	defer c.localServer.Unregister(id)

	// FFmpeg can now read from this fullURL just like a standard file
	return c.generatePreview(ctx, fullURL, outputWriter, inputMimeType)
}

// generatePreview contains the core FFmpeg execution logic shared by both file and stream inputs.
func (c *FfmpegConverter) generatePreview(ctx context.Context, inputSource string, outputWriter io.Writer, inputMimeType string) error {
	ffmpegPath, err := c.GetFFmpegPath()
	if err != nil {
		return fmt.Errorf("ffmpeg is not available: %w", err)
	}

	contentType := media.GetContentType(inputMimeType)

	var filterArgs []string

	switch contentType {
	case "image":
		// Crop to aspect ratio [0.4, 2.5] then scale to fit 200x200
		filterArgs = []string{
			"-vframes", "1",
			"-vf", fmt.Sprintf("crop=min(iw\\,2.5*ih):min(ih\\,2.5*iw),scale='%d:%d':force_original_aspect_ratio=decrease", maxPreviewWidth, maxPreviewHeight),
		}
	case "video":
		// Use thumbnail filter (n=30) to select the most representative frame, avoiding black start frames safely even for videos < 1s
		filterArgs = []string{
			"-vframes", "1",
			"-vf", fmt.Sprintf("thumbnail=n=30,crop=min(iw\\,2.5*ih):min(ih\\,2.5*iw),scale='%d:%d':force_original_aspect_ratio=decrease", maxPreviewWidth, maxPreviewHeight),
		}
	case "audio":
		// Generate a 200x120 waveform image (using a pleasant blue color)
		filterArgs = []string{
			"-filter_complex", "showwavespic=s=200x120:colors=#1E90FF",
			"-frames:v", "1",
		}
	case "file":
		// Standard files do not support previews
		return fmt.Errorf("preview generation is not supported for generic files")
	default:
		return fmt.Errorf("unknown content type for preview: %s", contentType)
	}

	// Assemble the final FFmpeg command arguments
	args := []string{"-v", "error"} // Only output fatal errors to keep logs clean
	args = append(args, "-i", inputSource)
	args = append(args, filterArgs...)

	// Force the output format to WebP using image2pipe to ensure it can be piped safely without seeking
	args = append(args, "-c:v", "libwebp", "-f", "image2pipe", "pipe:1")

	// Bind the FFmpeg process to the provided context
	cmd := exec.CommandContext(ctx, ffmpegPath, args...)

	// Pipe the WebP binary data directly into the provided outputWriter
	cmd.Stdout = outputWriter

	// Capture standard error for debugging purposes
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		c.logger.Error("FFmpeg preview generation failed",
			"error", err,
			"stderr", stderr.String(),
			"source", inputSource,
			"mimetype", inputMimeType,
		)
		return fmt.Errorf("ffmpeg preview error: %w", err)
	}

	return nil
}
