package processing

import (
	"context"
	"io"
	"os"
	"testing"

	"mediahub_oss/internal/media"
	repo "mediahub_oss/internal/repository"
)

type mockConverter struct {
	lastPreviewMimeCheck string
	canPreviewFunc       func(mime string) bool
	canConvertFunc       func(inputMime string, opts media.ConversionOptions) media.ConversionCheck
}

func (m *mockConverter) GetOutputMimeTypes(contentType string) []string {
	return []string{"audio/mpeg", "image/webp"}
}

func (m *mockConverter) CanCreatePreview(inputMimeType string) bool {
	m.lastPreviewMimeCheck = inputMimeType
	if m.canPreviewFunc != nil {
		return m.canPreviewFunc(inputMimeType)
	}
	return true
}

func (m *mockConverter) CanConvert(inputMimeType string, opts media.ConversionOptions) media.ConversionCheck {
	if m.canConvertFunc != nil {
		return m.canConvertFunc(inputMimeType, opts)
	}
	if inputMimeType == opts.TargetMimeType && opts.Width == 0 && opts.Height == 0 {
		return media.ConversionCheck{CanConvert: true, NeedsConversion: false}
	}
	return media.ConversionCheck{CanConvert: true, NeedsConversion: true}
}

func (m *mockConverter) ConvertStreamToFile(ctx context.Context, inputData io.ReadSeeker, inputMimeType string, opts media.ConversionOptions) (*os.File, error) {
	tmp, err := os.CreateTemp("", "mock-stream-*.tmp")
	if err != nil {
		return nil, err
	}
	_, err = io.Copy(tmp, inputData)
	if err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return nil, err
	}
	tmp.Seek(0, io.SeekStart)
	return tmp, nil
}

func (m *mockConverter) ConvertFile(ctx context.Context, inputPath string, outputPath string, inputMimeType string, opts media.ConversionOptions) error {
	return nil
}

func (m *mockConverter) ReadMediaFieldsFromStream(ctx context.Context, inputData io.ReadSeeker, contentType string) (map[string]any, error) {
	return map[string]any{}, nil
}

func (m *mockConverter) ReadMediaFieldsFromFile(ctx context.Context, filepath string, contentType string) (map[string]any, error) {
	return map[string]any{}, nil
}

func (m *mockConverter) CreatePreviewFromStream(ctx context.Context, inputData io.ReadSeeker, outputWriter io.Writer, inputMimeType string) error {
	return nil
}

func (m *mockConverter) CreatePreviewFromFile(ctx context.Context, filepath string, outputWriter io.Writer, inputMimeType string) error {
	return nil
}

func TestDetermineConversionPlan(t *testing.T) {
	mc := &mockConverter{}
	db := repo.Database{
		ContentType: "audio",
		Config: repo.DatabaseConfig{
			AutoConversion: "audio/mpeg",
			CreatePreview:  false,
		},
	}

	plan, err := DetermineConversionPlan(mc, db, "audio/wav", "recording.wav", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if plan.InitFileName != "recording.wav" {
		t.Errorf("InitFileName = %q; want %q", plan.InitFileName, "recording.wav")
	}
	if plan.FinalFileName != "recording.mp3" {
		t.Errorf("FinalFileName = %q; want %q", plan.FinalFileName, "recording.mp3")
	}
	if plan.InitMimeType != "audio/wav" {
		t.Errorf("InitMimeType = %q; want %q", plan.InitMimeType, "audio/wav")
	}
	if plan.ResultMimeType != "audio/mpeg" {
		t.Errorf("ResultMimeType = %q; want %q", plan.ResultMimeType, "audio/mpeg")
	}
}

func TestDeterminePlanForEntry(t *testing.T) {
	mc := &mockConverter{}
	db := repo.Database{
		ContentType: "audio",
		Config: repo.DatabaseConfig{
			AutoConversion: "audio/mpeg",
			CreatePreview:  false,
		},
	}
	entry := repo.Entry{
		FileName: "song.wav",
		MimeType: "audio/wav",
	}

	plan := DeterminePlanForEntry(mc, db, entry)
	if plan.InitFileName != "song.wav" {
		t.Errorf("InitFileName = %q; want %q", plan.InitFileName, "song.wav")
	}
	if plan.FinalFileName != "song.mp3" {
		t.Errorf("FinalFileName = %q; want %q", plan.FinalFileName, "song.mp3")
	}
}

func TestDeterminePlan_ShorthandAutoConversionNormalized(t *testing.T) {
	mc := &mockConverter{}
	db := repo.Database{
		ContentType: "audio",
		Config: repo.DatabaseConfig{
			AutoConversion: "flac",
			CreatePreview:  false,
		},
	}

	plan, err := DetermineConversionPlan(mc, db, "audio/wav", "track.wav", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.TargetMimeType != "audio/flac" {
		t.Errorf("TargetMimeType = %q; want %q", plan.TargetMimeType, "audio/flac")
	}
	if plan.ResultMimeType != "audio/flac" {
		t.Errorf("ResultMimeType = %q; want %q", plan.ResultMimeType, "audio/flac")
	}
	if plan.FinalFileName != "track.flac" {
		t.Errorf("FinalFileName = %q; want %q", plan.FinalFileName, "track.flac")
	}

	entryPlan := DeterminePlanForEntry(mc, db, repo.Entry{
		FileName: "track.wav",
		MimeType: "wav",
	})
	if entryPlan.InitMimeType != "audio/wav" {
		t.Errorf("InitMimeType = %q; want %q", entryPlan.InitMimeType, "audio/wav")
	}
	if entryPlan.TargetMimeType != "audio/flac" {
		t.Errorf("TargetMimeType = %q; want %q", entryPlan.TargetMimeType, "audio/flac")
	}
	if entryPlan.ResultMimeType != "audio/flac" {
		t.Errorf("ResultMimeType = %q; want %q", entryPlan.ResultMimeType, "audio/flac")
	}
	if entryPlan.FinalFileName != "track.flac" {
		t.Errorf("FinalFileName = %q; want %q", entryPlan.FinalFileName, "track.flac")
	}
}

func TestDeterminePlan_CanGenPreviewUsesResultMimeType(t *testing.T) {
	mc := &mockConverter{
		canPreviewFunc: func(mime string) bool {
			// Simulate a converter that can preview image/png (result) but not image/jpeg (input).
			return mime == "image/png"
		},
	}
	db := repo.Database{
		ContentType: "image",
		Config: repo.DatabaseConfig{
			AutoConversion: "image/png",
			CreatePreview:  true,
		},
	}

	plan, err := DetermineConversionPlan(mc, db, "image/jpeg", "scan.jpg", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mc.lastPreviewMimeCheck != "image/png" {
		t.Errorf("CanCreatePreview called with %q; want %q (ResultMimeType)", mc.lastPreviewMimeCheck, "image/png")
	}
	if !plan.CanGenPreview {
		t.Errorf("expected CanGenPreview = true when ResultMimeType supports preview")
	}
	if !plan.NeedsSlot() {
		t.Errorf("expected NeedsSlot() = true when conversion/preview is active")
	}
}

func TestDetermineConversionPlan_CannotConvertReturnsError(t *testing.T) {
	mc := &mockConverter{
		canConvertFunc: func(inputMime string, opts media.ConversionOptions) media.ConversionCheck {
			return media.ConversionCheck{CanConvert: false, NeedsConversion: true}
		},
	}
	db := repo.Database{
		ContentType: "image",
		Config: repo.DatabaseConfig{
			AutoConversion: "image/webp",
		},
	}

	_, err := DetermineConversionPlan(mc, db, "image/jpeg", "photo.jpg", "")
	if err == nil {
		t.Fatal("expected error from DetermineConversionPlan when NeedsConversion && !CanConvert, got nil")
	}
}
