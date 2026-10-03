package processing

import (
	"path/filepath"
	"strings"

	"mediahub_oss/internal/media"
	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared/customerrors"
)

// ProcessingPlan holds the details needed for file conversion and preview generation.
type ProcessingPlan struct {
	WantsConversion bool // database is configured with an auto-conversion target
	NeedsConversion bool // the converter would actually transform the file
	CanConvert      bool // the converter is capable of performing the conversion

	WantsPreview  bool // database is configured to create previews
	CanGenPreview bool // the converter can generate a preview for the input type

	InitMimeType   string // normalized MIME type of the uploaded file
	TargetMimeType string // configured auto-conversion target (normalized)
	ResultMimeType string // MIME type of the finally stored file

	InitFileName  string // file name before conversion (user name preferred)
	FinalFileName string // file name after a potential conversion (extension adjusted)
}

// determinePlan computes the processing plan for a file with the given MIME type
// and file name. If userFileName is non-empty, it overrides the original file name
// (inheriting the original extension when missing).
func determinePlan(mc media.MediaConverter, db repo.Database, originalMimeType, originalFileName, userFileName string) ProcessingPlan {
	originalMimeType = media.NormalizeMimeType(originalMimeType)

	targetMimeType := originalMimeType
	resultMimeType := originalMimeType

	wantsConversion := db.Config.AutoConversion != ""
	var convCheck media.ConversionCheck
	if wantsConversion {
		targetMimeType = media.NormalizeMimeType(db.Config.AutoConversion)
		convCheck = mc.CanConvert(originalMimeType, media.ConversionOptions{TargetMimeType: targetMimeType})
		if convCheck.CanConvert && convCheck.NeedsConversion {
			// Only when a conversion will actually be performed does the stored
			// file end up in the target format.
			resultMimeType = targetMimeType
		}
	}

	initFileName := originalFileName
	if userFileName != "" {
		initFileName = userFileName
		if filepath.Ext(initFileName) == "" {
			initFileName += filepath.Ext(originalFileName)
		}
	}

	finalFileName := initFileName
	if convCheck.NeedsConversion && convCheck.CanConvert {
		finalFileName = ReplaceExtension(finalFileName, GetExtensionForMimeType(targetMimeType))
	}

	return ProcessingPlan{
		WantsConversion: wantsConversion,
		NeedsConversion: convCheck.NeedsConversion,
		CanConvert:      convCheck.CanConvert,
		WantsPreview:    db.Config.CreatePreview,
		CanGenPreview:   mc.CanCreatePreview(originalMimeType),
		InitMimeType:    originalMimeType,
		TargetMimeType:  targetMimeType,
		ResultMimeType:  resultMimeType,
		InitFileName:    initFileName,
		FinalFileName:   finalFileName,
	}
}

// DetermineConversionPlan evaluates the processing plan for a newly uploaded file.
// It returns customerrors.ErrBadMimeType if the file's MIME type does not match
// the database content type.
func DetermineConversionPlan(mc media.MediaConverter, db repo.Database, originalMimeType string, originalFileName string, userFileName string) (ProcessingPlan, error) {
	normalizedMime := media.NormalizeMimeType(originalMimeType)

	isValid, err := media.IsMimeOfType(db.ContentType, normalizedMime)
	if err != nil {
		return ProcessingPlan{InitMimeType: normalizedMime}, err
	}
	if !isValid {
		return ProcessingPlan{InitMimeType: normalizedMime}, customerrors.ErrBadMimeType
	}

	return determinePlan(mc, db, normalizedMime, originalFileName, userFileName), nil
}

// DeterminePlanForEntry evaluates the processing plan for an entry that is already
// stored in the database (e.g. a queued entry picked up by a worker). The MIME type
// was validated at upload time, so no validation error can occur here.
func DeterminePlanForEntry(mc media.MediaConverter, db repo.Database, entry repo.Entry) ProcessingPlan {
	return determinePlan(mc, db, entry.MimeType, entry.FileName, "")
}

// GetExtensionForMimeType returns the preferred file extension for a given MIME type (e.g., ".opus")
func GetExtensionForMimeType(mimeType string) string {
	var mimeToExtension = map[string]string{
		// Images
		"image/jpeg": "jpg",
		"image/png":  "png",
		"image/gif":  "gif",
		"image/webp": "webp",
		"image/avif": "avif",

		// Audio
		"audio/mpeg":      "mp3",
		"audio/wav":       "wav",
		"audio/flac":      "flac",
		"audio/x-flac":    "flac",
		"audio/opus":      "opus",
		"audio/ogg":       "ogg",
		"application/ogg": "ogg",

		// Video
		"video/mp4":  "mp4",
		"video/webm": "webm",
		"video/ogg":  "ogv",
	}

	if ext, ok := mimeToExtension[mimeType]; ok {
		return "." + ext
	}

	// Fallback for unmapped types (e.g., "text/plain" -> ".plain")
	parts := strings.Split(mimeType, "/")
	if len(parts) == 2 {
		ext := strings.TrimPrefix(parts[1], "x-")
		return "." + ext
	}

	return ""
}

// ReplaceExtension replaces the extension of a filename with a new one.
func ReplaceExtension(filename string, newExt string) string {
	if filename == "" {
		return ""
	}
	if newExt == "" {
		return filename
	}
	ext := filepath.Ext(filename)
	base := strings.TrimSuffix(filename, ext)
	return base + newExt
}
