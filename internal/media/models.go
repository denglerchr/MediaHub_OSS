package media

// FieldDef defines a media-specific metadata field and its database type.
type FieldDef struct {
	Name string
	Type string // e.g., "int64", "float64", "string", ...
}

type ConversionCheck struct {
	NeedsConversion bool // false if mime types are aliases or are equal and no resize/fit is requested
	CanConvert      bool // indicates capability to convert to target
}

// ConversionOptions holds parameters for format conversion, resizing, and scaling.
type ConversionOptions struct {
	TargetMimeType string
	Width          int
	Height         int
	Fit            string // "cut", "stretch", "pad-white", "pad-black"
}

var imageMimeTypes = []string{
	"image/png",
	"image/jpeg",
	"image/webp",
	"image/gif",
	"image/avif",
}

var videoMimeTypes = []string{
	"video/mp4",
	"video/webm",
	"video/ogg",
	"video/x-matroska",
	"video/x-msvideo",
	"video/x-flv",
	"video/quicktime",
}

var audioMimeTypes = []string{
	"audio/mpeg",
	"audio/mp3",
	"audio/wav",
	"audio/flac",
	"audio/opus",
	"audio/ogg",
	"audio/mp4",
	"audio/m4a",
}
