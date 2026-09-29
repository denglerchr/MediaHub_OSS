package processing

import (
	"fmt"
	"path/filepath"
	"strings"

	"mediahub_oss/internal/media"
	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared/customerrors"
)

// ProcessingPlan holds the details needed for file conversion and preview generation.
type ProcessingPlan struct {
	WantsConversion bool
	NeedsConversion bool
	CanConvert      bool

	WantsPreview  bool
	CanGenPreview bool

	InitMimeType   string
	TargetMimeType string
	ResultMimeType string

	InitFileName  string
	FinalFileName string
}

// DetermineConversionPlan evaluates if a file needs conversion based on the database configuration.
func DetermineConversionPlan(mc media.MediaConverter, db repo.Database, originalMimeType string, originalFileName string, userFileName string) (ProcessingPlan, error) {
	originalMimeType = media.NormalizeMimeType(originalMimeType)

	isValid, err := media.IsMimeOfType(db.ContentType, originalMimeType)
	if !isValid {
		return ProcessingPlan{InitMimeType: originalMimeType}, customerrors.ErrBadMimeType
	}
	if err != nil {
		return ProcessingPlan{InitMimeType: originalMimeType}, err
	}

	wantsConversion := (db.Config.AutoConversion != "")
	targetMimeType := originalMimeType
	resultMimeType := originalMimeType

	var convCheck media.ConversionCheck
	if wantsConversion {
		targetMimeType = db.Config.AutoConversion

		// check capabilities
		convCheck = mc.CanConvert(originalMimeType, media.ConversionOptions{TargetMimeType: db.Config.AutoConversion})
		if convCheck.CanConvert {
			resultMimeType = targetMimeType
		}
	}

	canGenPreview := mc.CanCreatePreview(originalMimeType)

	// derive file name
	initFileName := originalFileName
	if userFileName != "" {
		initFileName = userFileName
		if filepath.Ext(initFileName) == "" {
			originalExt := filepath.Ext(originalFileName)
			initFileName = initFileName + originalExt
		}
	}

	finalFileName := initFileName
	if convCheck.NeedsConversion && convCheck.CanConvert {
		newExtension := GetExtensionForMimeType(db.Config.AutoConversion)
		finalFileName = ReplaceExtension(finalFileName, newExtension)
	}

	return ProcessingPlan{
		WantsConversion: wantsConversion,
		NeedsConversion: convCheck.NeedsConversion,
		CanConvert:      convCheck.CanConvert,
		WantsPreview:    db.Config.CreatePreview,
		CanGenPreview:   canGenPreview,
		InitMimeType:    originalMimeType,
		TargetMimeType:  targetMimeType,
		ResultMimeType:  resultMimeType,
		InitFileName:    initFileName,
		FinalFileName:   finalFileName,
	}, nil
}

// DeterminePlanForEntry determines the processing plan for a queued/processing database entry.
func DeterminePlanForEntry(mc media.MediaConverter, db repo.Database, entry repo.Entry) ProcessingPlan {
	originalMimeType := entry.MimeType
	wantsConversion := (db.Config.AutoConversion != "")
	targetMimeType := originalMimeType
	resultMimeType := originalMimeType

	var convCheck media.ConversionCheck
	if wantsConversion {
		targetMimeType = db.Config.AutoConversion
		convCheck = mc.CanConvert(originalMimeType, media.ConversionOptions{TargetMimeType: db.Config.AutoConversion})
		if convCheck.CanConvert {
			resultMimeType = targetMimeType
		}
	}

	canGenPreview := mc.CanCreatePreview(originalMimeType)

	// Derive initial and final file names
	initFileName := entry.FileName
	finalFileName := initFileName
	if convCheck.NeedsConversion && convCheck.CanConvert {
		newExtension := GetExtensionForMimeType(db.Config.AutoConversion)
		finalFileName = ReplaceExtension(finalFileName, newExtension)
	}

	return ProcessingPlan{
		WantsConversion: wantsConversion,
		NeedsConversion: convCheck.NeedsConversion,
		CanConvert:      convCheck.CanConvert,
		WantsPreview:    db.Config.CreatePreview,
		CanGenPreview:   canGenPreview,
		InitMimeType:    originalMimeType,
		TargetMimeType:  targetMimeType,
		ResultMimeType:  resultMimeType,
		InitFileName:    initFileName,
		FinalFileName:   finalFileName,
	}
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

// DefaultMediaFields returns dynamic defaults for media fields based on content type.
func DefaultMediaFields(contentType string) (map[string]any, error) {
	var val any

	metadataFields, err := media.GetMetadataFields(contentType)
	if err != nil {
		return nil, fmt.Errorf("failed to get metadata fields: %w", err)
	}

	mediaFields := make(map[string]any)
	for _, field := range metadataFields {
		switch field.Type {
		case "uint8":
			val = uint8(0)
		case "uint64":
			val = uint64(0)
		case "int64":
			val = int64(-1)
		case "float64":
			val = float64(-1.0)
		case "bool":
			val = false
		default:
			return nil, fmt.Errorf("implementation missing default value for media field type %s", field.Type)
		}
		mediaFields[field.Name] = val
	}

	return mediaFields, nil
}
