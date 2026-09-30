package ffmpeg

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"mediahub_oss/internal/shared/customerrors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type FfmpegConverter struct {
	ffmpegPath           string
	ffprobePath          string
	logger               *slog.Logger
	supportedConversions map[string]ConversionProfile
	localServer          *LocalStreamServer
}

// Updated signature: now returns a pointer and an error
func NewFFMPEGConverter(ffmpegConfiguredPath string, ffprobeConfiguredPath string, logger *slog.Logger) (*FfmpegConverter, error) {
	var ffmpegPath string = ""
	var ffprobePath string = ""

	// --- FFmpeg Check ---
	if ffmpegConfiguredPath != "" {
		if _, err := os.Stat(ffmpegConfiguredPath); err == nil {
			logger.Info("Using configured FFmpeg path", "path", ffmpegConfiguredPath)
			ffmpegPath = ffmpegConfiguredPath
		} else {
			logger.Warn("Configured ffmpeg_path not found, falling back to system PATH.", "config_path", ffmpegConfiguredPath)
		}
	}

	if ffmpegPath == "" { // Only check PATH if not configured
		path, err := exec.LookPath("ffmpeg")
		if err != nil {
			logger.Warn("---------------------------------------------------------")
			logger.Warn("FFmpeg executable not found in configured path or system PATH.")
			logger.Warn("Auto-conversions will be DISABLED.")
			logger.Warn("---------------------------------------------------------")
			ffmpegPath = "" // Explicitly set to empty
		} else {
			logger.Info("FFmpeg found in PATH. Media conversion enabled.", "path", path)
			ffmpegPath = path
		}
	}

	// --- FFprobe Check ---
	if ffprobeConfiguredPath != "" {
		if _, err := os.Stat(ffprobeConfiguredPath); err == nil {
			logger.Info("Using configured FFprobe path", "path", ffprobeConfiguredPath)
			ffprobePath = ffprobeConfiguredPath
		} else {
			logger.Warn("Configured ffprobe_path not found, falling back to system PATH.", "path", ffprobeConfiguredPath)
		}
	}

	if ffprobePath == "" { // Only check PATH if not found or configured
		if ffmpegPath != "" {
			base := filepath.Base(ffmpegPath)
			if strings.Contains(base, "ffmpeg") {
				probePath := filepath.Join(filepath.Dir(ffmpegPath), strings.Replace(base, "ffmpeg", "ffprobe", 1))
				if _, err := os.Stat(probePath); err == nil {
					logger.Info("Found ffprobe alongside ffmpeg in PATH", "path", probePath)
					ffprobePath = probePath
				}
			}
		}
	}

	if ffprobePath == "" { // Still not found? Check PATH explicitly.
		path, err := exec.LookPath("ffprobe")
		if err != nil {
			logger.Warn("---------------------------------------------------------")
			logger.Warn("ffprobe executable not found. Metadata extraction will be disabled.")
			logger.Warn("---------------------------------------------------------")
			ffprobePath = ""
		} else {
			logger.Info("ffprobe found in PATH. Metadata extraction enabled.", "path", path)
			ffprobePath = path
		}
	}

	// --- Initialize the Local Stream Server ---
	streamServer, err := NewLocalStreamServer(logger)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize internal loopback server: %w", err)
	}

	converter := &FfmpegConverter{
		ffmpegPath:  ffmpegPath,
		ffprobePath: ffprobePath,
		logger:      logger,
		localServer: streamServer,
	}

	// Probe FFmpeg and set up hardware acceleration
	converter.initConversions()

	return converter, nil
}

// Ensure you add a Shutdown method so you can cleanly stop the loopback server when your app closes
func (ffmpegc *FfmpegConverter) Shutdown(ctx context.Context) error {
	if ffmpegc.localServer != nil {
		return ffmpegc.localServer.Shutdown(ctx)
	}
	return nil
}

// IsFFmpegAvailable checks if the ffmpeg executable path was successfully found.
func (ffmpegc *FfmpegConverter) IsFFmpegAvailable() bool {
	return ffmpegc.ffmpegPath != ""
}

// GetFFmpegPath returns the determined path to the ffmpeg executable.
func (ffmpegc *FfmpegConverter) GetFFmpegPath() (string, error) {
	if ffmpegc.IsFFmpegAvailable() {
		return ffmpegc.ffmpegPath, nil
	} else {
		return "", customerrors.ErrNotFound
	}
}

// IsFFprobeAvailable checks if the ffprobe executable path was successfully found.
func (ffmpegc *FfmpegConverter) IsFFprobeAvailable() bool {
	return ffmpegc.ffprobePath != ""
}

// GetFFprobePath returns the determined path to the ffprobe executable.
func (ffmpegc *FfmpegConverter) GetFFprobePath() (string, error) {
	if ffmpegc.IsFFprobeAvailable() {
		return ffmpegc.ffprobePath, nil
	} else {
		return "", customerrors.ErrNotFound
	}
}

// localFilePath checks if an io.ReadSeeker is backed by an accessible local *os.File.
func localFilePath(r io.ReadSeeker) (string, bool) {
	if f, ok := r.(*os.File); ok && f != nil && f.Name() != "" {
		if _, err := os.Stat(f.Name()); err == nil {
			return f.Name(), true
		}
	}
	return "", false
}
