package processing

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"

	"mediahub_oss/internal/media"
	"mediahub_oss/internal/shared/customerrors"
)

// TempFileStream wraps an open temporary *os.File and an optional onClose cleanup
// callback. Calling Close() closes the underlying file handle, removes the
// temporary file from disk, and invokes onClose exactly once (e.g. to release a
// reserved conversion slot, which triggers background queue workers).
type TempFileStream struct {
	*os.File
	onClose func()
}

// Close closes the file handle, deletes the underlying temp file, and invokes the
// onClose callback.
func (t *TempFileStream) Close() error {
	var err error
	if t.File != nil {
		name := t.File.Name()
		err = t.File.Close()
		os.Remove(name)
		t.File = nil
	}
	if t.onClose != nil {
		t.onClose()
		t.onClose = nil
	}
	return err
}

// ProcessOutgoingEntry is the entry point for on-the-fly transformation of a stored
// entry's file content ("outgoing" pipeline), e.g. when a client downloads an entry
// with a different target format or resolution.
//
// It reserves a synchronous conversion slot, converts the input stream according
// to the provided options into a temporary file, and returns it as an open stream
// together with the exact result size in bytes. The slot is released when the
// caller closes the returned stream.
//
// Returns customerrors.ErrUnavailable if all conversion slots are currently occupied.
func (p *Processor) ProcessOutgoingEntry(
	ctx context.Context,
	input io.Reader,
	inputMimeType string,
	opts media.ConversionOptions,
) (*TempFileStream, int64, error) {
	if !p.tryReserveSyncSlot() {
		return nil, 0, customerrors.ErrUnavailable
	}

	stream, size, err := p.convertToTempFile(ctx, input, inputMimeType, opts)
	if err != nil {
		p.releaseSyncSlot()
		return nil, 0, err
	}

	stream.onClose = p.releaseSyncSlot
	return stream, size, nil
}

// convertToTempFile converts an input stream into a temporary file using
// MediaConverter.ConvertStreamToFile without reserving a conversion slot.
// It returns the open TempFileStream and its size in bytes.
func (p *Processor) convertToTempFile(
	ctx context.Context,
	input io.Reader,
	inputMimeType string,
	opts media.ConversionOptions,
) (*TempFileStream, int64, error) {
	var seeker io.ReadSeeker
	if rs, ok := input.(io.ReadSeeker); ok {
		if _, err := rs.Seek(0, io.SeekStart); err != nil {
			return nil, 0, fmt.Errorf("failed to seek input stream: %w", err)
		}
		seeker = rs
	} else {
		data, err := io.ReadAll(input)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to read input stream: %w", err)
		}
		seeker = bytes.NewReader(data)
	}

	tempFile, err := p.MediaConverter.ConvertStreamToFile(ctx, seeker, inputMimeType, opts)
	if err != nil {
		return nil, 0, fmt.Errorf("stream conversion failed: %w", err)
	}

	stat, err := tempFile.Stat()
	if err != nil {
		tempFile.Close()
		os.Remove(tempFile.Name())
		return nil, 0, fmt.Errorf("failed to stat converted temporary file: %w", err)
	}

	return &TempFileStream{File: tempFile}, stat.Size(), nil
}
