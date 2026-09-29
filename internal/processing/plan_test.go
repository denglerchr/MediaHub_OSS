package processing

import (
	"context"
	"io"
	"os"
	"testing"

	"mediahub_oss/internal/media"
	repo "mediahub_oss/internal/repository"
)

type mockConverter struct{}

func (m *mockConverter) GetOutputMimeTypes(contentType string) []string {
	return []string{"audio/mpeg", "image/webp"}
}

func (m *mockConverter) CanCreatePreview(inputMimeType string) bool {
	return true
}

func (m *mockConverter) CanConvert(inputMimeType string, opts media.ConversionOptions) media.ConversionCheck {
	if inputMimeType == opts.TargetMimeType && opts.Width == 0 && opts.Height == 0 {
		return media.ConversionCheck{CanConvert: true, NeedsConversion: false}
	}
	return media.ConversionCheck{CanConvert: true, NeedsConversion: true}
}

func (m *mockConverter) ConvertStream(ctx context.Context, inputData io.ReadSeeker, outputStream io.Writer, inputMimeType string, opts media.ConversionOptions) error {
	return nil
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
