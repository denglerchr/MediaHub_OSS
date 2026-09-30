package ffmpeg

import (
	"mediahub_oss/internal/media"
	"sort"
	"strings"
)

// GetOutputMimeTypes dynamically returns recommended target formats based on our supportedConversions map.
func (c *FfmpegConverter) GetOutputMimeTypes(contentType string) []string {
	outputs := make([]string, 0, len(c.supportedConversions)) // Micro-optimization: pre-allocate capacity
	for mime, profile := range c.supportedConversions {
		if profile.ContentType == contentType && profile.IsRecommended {
			outputs = append(outputs, mime)
		}
	}

	// Sort to ensure the API response is consistent, as Go map iteration is random
	sort.Strings(outputs)
	return outputs
}

// CanCreatePreview determines if FFmpeg can generate a visual preview for this file.
func (c *FfmpegConverter) CanCreatePreview(inputMimeType string) bool {
	if !c.IsFFmpegAvailable() {
		return false
	}

	normalized := media.NormalizeMimeType(inputMimeType)

	// Return the evaluation directly
	return strings.HasPrefix(normalized, "image/") ||
		strings.HasPrefix(normalized, "video/") ||
		strings.HasPrefix(normalized, "audio/")
}

// CanConvert checks if a conversion is possible based on our supportedConversions map and options.
func (c *FfmpegConverter) CanConvert(inputMimeType string, opts media.ConversionOptions) media.ConversionCheck {
	normInput := media.NormalizeMimeType(inputMimeType)

	normTarget := opts.TargetMimeType
	if normTarget == "" {
		normTarget = normInput
	} else {
		normTarget = media.NormalizeMimeType(normTarget)
	}

	// Check if conversion is needed: either format changes or image resizing is requested
	needsConversion := (normInput != normTarget) || opts.Width > 0 || opts.Height > 0
	canConvert := false

	// Check if we can convert
	if c.IsFFmpegAvailable() {
		contentType := media.GetContentType(normInput)

		if contentType != "file" {
			// Video and audio do not support resolution resizing or fit options
			if (contentType == "video" || contentType == "audio") && (opts.Width > 0 || opts.Height > 0 || opts.Fit != "") {
				canConvert = false
			} else {
				if profile, exists := c.supportedConversions[normTarget]; exists && profile.ContentType == contentType {
					canConvert = true
				}
			}
		}
	}

	return media.ConversionCheck{
		NeedsConversion: needsConversion,
		CanConvert:      canConvert,
	}
}
