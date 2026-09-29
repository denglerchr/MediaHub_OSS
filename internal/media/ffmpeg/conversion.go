package ffmpeg

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"mediahub_oss/internal/media"
)

// ConversionProfile defines the FFmpeg arguments required for a specific output format.
type ConversionProfile struct {
	ContentType string
	Args        []string
}

// BuildImageFilter constructs the FFmpeg video filter (-vf) string for image resizing and scaling.
// Supported fit modes:
//   - "cut" (default when both width & height given): Preserves aspect ratio, scales to cover, crops overflow.
//   - "stretch": Forces exact width & height, ignoring aspect ratio.
//   - "pad-white": Preserves aspect ratio, fits within target dimensions, pads with white background.
//   - "pad-black": Preserves aspect ratio, fits within target dimensions, pads with black background.
// Single dimension scaling (width only or height only) scales proportionally preserving aspect ratio.
func BuildImageFilter(width, height int, fit string) (string, error) {
	if width < 0 || height < 0 {
		return "", fmt.Errorf("width and height must be non-negative")
	}
	if width == 0 && height == 0 {
		return "", nil
	}

	// Single dimension: proportional scaling preserving aspect ratio
	if width > 0 && height == 0 {
		return fmt.Sprintf("scale=w=%d:h=-1", width), nil
	}
	if width == 0 && height > 0 {
		return fmt.Sprintf("scale=w=-1:h=%d", height), nil
	}

	// Both dimensions provided
	switch strings.ToLower(strings.TrimSpace(fit)) {
	case "cut", "":
		return fmt.Sprintf("scale=w=%d:h=%d:force_original_aspect_ratio=increase,crop=%d:%d", width, height, width, height), nil
	case "stretch":
		return fmt.Sprintf("scale=w=%d:h=%d", width, height), nil
	case "pad-white":
		return fmt.Sprintf("scale=w=%d:h=%d:force_original_aspect_ratio=decrease,pad=w=%d:h=%d:x=(ow-iw)/2:y=(oh-ih)/2:color=white", width, height, width, height), nil
	case "pad-black":
		return fmt.Sprintf("scale=w=%d:h=%d:force_original_aspect_ratio=decrease,pad=w=%d:h=%d:x=(ow-iw)/2:y=(oh-ih)/2:color=black", width, height, width, height), nil
	default:
		return "", fmt.Errorf("unsupported fit mode: %s", fit)
	}
}

// prepareConversion validates the conversion options and returns the normalized target MIME type
// and the combined processing arguments (filters and format/codec flags).
func (c *FfmpegConverter) prepareConversion(inputMimeType string, opts media.ConversionOptions) (string, []string, error) {
	contentType, err := media.GetContentType(inputMimeType)
	if err != nil {
		return "", nil, fmt.Errorf("failed to determine content type: %w", err)
	}

	if contentType != "image" && (opts.Width > 0 || opts.Height > 0 || opts.Fit != "") {
		return "", nil, fmt.Errorf("resolution and fit options are only supported for images, got content type: %s", contentType)
	}

	targetMimeType := opts.TargetMimeType
	if targetMimeType == "" {
		targetMimeType = inputMimeType
	}
	normTarget := media.NormalizeMimeType(targetMimeType)

	var filterArgs []string
	if contentType == "image" && (opts.Width > 0 || opts.Height > 0) {
		filter, err := BuildImageFilter(opts.Width, opts.Height, opts.Fit)
		if err != nil {
			return "", nil, err
		}
		if filter != "" {
			filterArgs = []string{"-vf", filter}
		}
	}

	formatArgs, err := c.buildConversionArgs(normTarget)
	if err != nil {
		return "", nil, err
	}

	conversionArgs := make([]string, 0, len(filterArgs)+len(formatArgs))
	conversionArgs = append(conversionArgs, filterArgs...)
	conversionArgs = append(conversionArgs, formatArgs...)

	return normTarget, conversionArgs, nil
}

// runFFmpegCommand executes an FFmpeg process with context and logs any failures with standard error output.
func (c *FfmpegConverter) runFFmpegCommand(ctx context.Context, ffmpegPath string, args []string, logContext string, normTarget string) error {
	cmd := exec.CommandContext(ctx, ffmpegPath, args...)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		c.logger.Error(logContext, "error", err, "stderr", stderr.String(), "target", normTarget)
		return fmt.Errorf("ffmpeg conversion error: %w", err)
	}

	return nil
}

// ConvertFile transcodes a large file using pure disk-to-disk direct I/O.
func (c *FfmpegConverter) ConvertFile(ctx context.Context, inputPath string, outputPath string, inputMimeType string, opts media.ConversionOptions) error {
	ffmpegPath, err := c.GetFFmpegPath()
	if err != nil {
		return fmt.Errorf("ffmpeg is not available: %w", err)
	}

	normTarget, conversionArgs, err := c.prepareConversion(inputMimeType, opts)
	if err != nil {
		return err
	}

	args := append([]string{"-y", "-i", inputPath}, conversionArgs...)
	args = append(args, outputPath)

	return c.runFFmpegCommand(ctx, ffmpegPath, args, "FFmpeg file conversion failed", normTarget)
}

// ConvertStreamToFile executes the stream conversion and returns an open *os.File to the resulting temporary file.
// The caller is responsible for closing the returned file and removing it from disk.
func (c *FfmpegConverter) ConvertStreamToFile(ctx context.Context, inputData io.ReadSeeker, inputMimeType string, opts media.ConversionOptions) (*os.File, error) {
	ffmpegPath, err := c.GetFFmpegPath()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg is not available: %w", err)
	}

	normTarget, conversionArgs, err := c.prepareConversion(inputMimeType, opts)
	if err != nil {
		return nil, err
	}

	// Register the stream with the local loopback server.
	id, fullURL, err := c.localServer.Register(inputData, 30*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("failed to register stream: %w", err)
	}
	defer c.localServer.Unregister(id)

	// Create the highly optimized temporary file to satisfy FFmpeg's need for a seekable output
	tmpPath, err := createInMemoryFile("", "ffmpeg-output-*.tmp")
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary output file: %w", err)
	}

	args := append([]string{"-y", "-i", fullURL}, conversionArgs...)
	args = append(args, tmpPath)

	if err := c.runFFmpegCommand(ctx, ffmpegPath, args, "FFmpeg stream conversion failed", normTarget); err != nil {
		os.Remove(tmpPath)
		return nil, err
	}

	generatedFile, err := os.Open(tmpPath)
	if err != nil {
		os.Remove(tmpPath)
		return nil, fmt.Errorf("failed to open generated temporary file: %w", err)
	}

	return generatedFile, nil
}

// ConvertStream transcodes small files in RAM, utilizing the HTTP loopback server for input
// and streams the result directly into outputStream without Go heap buffering.
func (c *FfmpegConverter) ConvertStream(ctx context.Context, inputData io.ReadSeeker, outputStream io.Writer, inputMimeType string, opts media.ConversionOptions) error {
	generatedFile, err := c.ConvertStreamToFile(ctx, inputData, inputMimeType, opts)
	if err != nil {
		return err
	}
	defer func() {
		generatedFile.Close()
		os.Remove(generatedFile.Name())
	}()

	// Stream the data from our memory-backed file into the final output destination
	if _, err := io.Copy(outputStream, generatedFile); err != nil {
		return fmt.Errorf("failed to copy converted data to output stream: %w", err)
	}

	return nil
}

// buildConversionArgs safely retrieves a copy of the pre-computed FFmpeg arguments.
func (c *FfmpegConverter) buildConversionArgs(targetMimeType string) ([]string, error) {
	profile, exists := c.supportedConversions[targetMimeType]
	if !exists {
		return nil, fmt.Errorf("unsupported conversion target format: %s", targetMimeType)
	}

	// Create a fresh copy of the slice to prevent concurrent requests from accidentally mutating the base profile
	argsCopy := make([]string, len(profile.Args))
	copy(argsCopy, profile.Args)

	return argsCopy, nil
}

// initConversions dynamically builds the supported conversions map, prioritizing hardware encoders.
func (c *FfmpegConverter) initConversions() {
	c.supportedConversions = make(map[string]ConversionProfile)

	// stop if ffmpeg not available
	_, err := c.GetFFmpegPath()
	if err != nil {
		c.logger.Warn("FFmpeg not available, skipping conversion profile setup")
		return
	}

	// 1. Add static Image and Audio profiles (these rely heavily on standard software encoding)
	// 1. Image and Audio Profiles
	c.supportedConversions["image/jpeg"] = ConversionProfile{
		ContentType: "image",
		Args:        []string{"-c:v", "mjpeg", "-vframes", "1", "-f", "image2"},
	}
	c.supportedConversions["image/webp"] = ConversionProfile{
		ContentType: "image",
		Args:        []string{"-c:v", "libwebp", "-vframes", "1", "-f", "webp"},
	}
	c.supportedConversions["image/avif"] = ConversionProfile{
		ContentType: "image",
		Args: []string{
			"-c:v", "libaom-av1",
			"-still-picture", "1", // Tells the AV1 encoder it's a static image, not a 1-frame video
			"-vframes", "1", // PREVENTS ANIMATED LOOP FLICKERING: Forces exactly 1 frame
			"-pix_fmt", "yuv420p", // Ensures cross-browser chroma subsampling compatibility
			"-cpu-used", "6", // Speed scale is 0-8 (0 is slowest, 8 is fastest). 6 is the web sweet spot!
			"-row-mt", "1", // Enables Row-Based Multithreading (Use all CPU cores)
			"-f", "avif",
		},
	}
	c.supportedConversions["audio/flac"] = ConversionProfile{
		ContentType: "audio",
		Args:        []string{"-c:a", "flac", "-f", "flac"},
	}
	c.supportedConversions["audio/opus"] = ConversionProfile{
		ContentType: "audio",
		Args:        []string{"-c:a", "libopus", "-f", "opus"},
	}

	// 2. Detect available video encoders
	available := c.getAvailableEncoders()

	// 3. Configure H.264 (MP4)
	h264Enc := c.selectBestEncoder(available, []string{
		"h264_nvenc", "h264_qsv", "h264_videotoolbox", "h264_amf",
		"h264_mf",     // Windows standard
		"h264_vulkan", // Universal Vulkan fallback
		"h264_vaapi",
		"libx264", // Software fallback
	})
	mp4Args := []string{"-c:v", h264Enc, "-c:a", "aac", "-b:a", "192k"}
	mp4Args = append(mp4Args, getEncoderSpecificFlags(h264Enc)...)    // Add quality flags
	mp4Args = append(mp4Args, "-f", "mp4", "-movflags", "+faststart") // Add file/muxer flags

	c.supportedConversions["video/mp4"] = ConversionProfile{
		ContentType: "video",
		Args:        mp4Args,
	}
	if h264Enc != "libx264" {
		c.logger.Info("Hardware acceleration enabled", "format", "video/mp4", "encoder", h264Enc)
	}

	// 4. Configure Webm (AV1)
	av1Enc := c.selectBestEncoder(available, []string{
		"av1_nvenc", "av1_qsv", "av1_amf",
		"av1_mf",     // Windows standard
		"av1_vulkan", // Universal Vulkan fallback
		"libsvtav1",  // Software fallback
	})

	av1Args := []string{"-c:v", av1Enc, "-c:a", "libopus"}
	av1Args = append(av1Args, getEncoderSpecificFlags(av1Enc)...) // Add dynamic quality flags
	av1Args = append(av1Args, "-f", "webm")                       // Add file/muxer flags

	c.supportedConversions["video/webm"] = ConversionProfile{
		ContentType: "video",
		Args:        av1Args,
	}
	if av1Enc != "libsvtav1" {
		c.logger.Info("Hardware acceleration enabled", "format", "video/webm", "encoder", av1Enc)
	}
}
