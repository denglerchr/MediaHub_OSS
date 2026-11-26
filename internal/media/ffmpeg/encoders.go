package ffmpeg

import (
	"fmt"
	"os/exec"
	"strings"
)

// getAvailableEncoders asks FFmpeg for a list of all compiled encoders.
func (c *FfmpegConverter) getAvailableEncoders() string {
	if c.ffmpegPath == "" {
		return ""
	}
	cmd := exec.Command(c.ffmpegPath, "-hide_banner", "-encoders")
	out, err := cmd.Output()
	if err != nil {
		c.logger.Warn("Failed to probe ffmpeg encoders, falling back to software defaults", "error", err)
		return ""
	}
	return string(out)
}

// selectBestEncoder checks the FFmpeg output and runs a hardware test to ensure viability.
func (c *FfmpegConverter) selectBestEncoder(available string, preferences []string) string {
	for i, enc := range preferences {
		// The last option in the array is our software fallback (e.g., libx264).
		// We usually assume the software fallback works if it is compiled.
		isSoftwareFallback := i == len(preferences)-1

		searchStr := fmt.Sprintf(" %s ", enc)
		if strings.Contains(available, searchStr) {

			if isSoftwareFallback {
				return enc // Accept software fallback immediately
			}

			// It is compiled, but does the hardware actually support it?
			c.logger.Debug("Probing hardware encoder capability...", "encoder", enc)
			if c.testEncoder(enc) {
				return enc // Hardware test passed!
			} else {
				c.logger.Debug("Encoder compiled but unsupported by system hardware/drivers", "encoder", enc)
			}
		}
	}
	// Ultimate fallback, should ideally never be reached if preferences are set up right
	return preferences[len(preferences)-1]
}

// testEncoder runs a tiny, 1-frame dummy conversion to verify if the hardware actually supports the encoder.
func (c *FfmpegConverter) testEncoder(encoder string) bool {
	// -f lavfi -i color=... : Generates a blank video stream entirely in memory
	// -vframes 1            : Only process a single frame
	// -c:v <encoder>        : Try using our specific hardware encoder
	// -f null -             : Throw the output away (don't write to disk)
	cmd := exec.Command(c.ffmpegPath,
		"-v", "error", // Only output fatal errors to keep logs clean
		"-f", "lavfi", "-i", "color=size=128x128:rate=1:duration=1",
		"-vframes", "1",
		"-c:v", encoder,
		"-f", "null", "-",
	)

	err := cmd.Run()
	// If err is nil, the hardware successfully initialized and encoded the frame!
	return err == nil
}

// getEncoderSpecificFlags maps the selected encoder to its specific Quality and Profile flags.
// It hides hardware-specific quirks (like Windows MF crashing on string profiles) from the main pipeline.
func getEncoderSpecificFlags(encoder string) []string {
	var flags []string

	// 1. Determine the resolution-independent quality flags
	switch {
	case strings.Contains(encoder, "nvenc"):
		// Nvidia uses Constant Quality (-cq) with Variable Bitrate (-rc vbr)
		flags = []string{"-rc", "vbr", "-cq", "28"}
	case strings.Contains(encoder, "qsv"):
		// Intel QuickSync uses Global Quality
		flags = []string{"-global_quality", "25"}
	case strings.Contains(encoder, "videotoolbox"):
		// Apple VideoToolbox uses a 1-100 quality scale
		flags = []string{"-q:v", "50"}
	case strings.Contains(encoder, "amf"):
		// AMD uses Constant Quantization Parameter
		flags = []string{"-rc", "cqp", "-qp_i", "24", "-qp_p", "24"}
	case strings.Contains(encoder, "svtav1"):
		// AV1 needs a higher CRF to match H264/VP9 visual quality.
		flags = []string{"-crf", "32", "-preset", "8"}
	case encoder == "libx264":
		// Standard software encoder
		flags = []string{"-crf", "26"}
	default:
		// Fallback for VAAPI/Vulkan or unknown hardware.
		// Since we don't know the exact CQ flag, we fallback to a safe constrained bitrate.
		flags = []string{"-b:v", "5M", "-maxrate", "8M", "-bufsize", "16M"}
	}

	// 2. Safely apply the H.264 High Profile optimization
	// We ensure it is an H.264 encoder, and specifically bypass the buggy Windows h264_mf wrapper.
	if strings.HasPrefix(encoder, "h264_") || encoder == "libx264" {
		if encoder != "h264_mf" {
			flags = append(flags, "-profile:v", "high")
		}
	}

	return flags
}
