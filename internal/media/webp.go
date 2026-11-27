package media

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	// MaxPreviewBytes is the maximum allowed byte size for a WebP preview (128 KiB).
	MaxPreviewBytes = 128 << 10
	// MaxPreviewWidth is the maximum allowed width in pixels for a WebP preview.
	MaxPreviewWidth = 200
	// MaxPreviewHeight is the maximum allowed height in pixels for a WebP preview.
	MaxPreviewHeight = 200
)

var (
	// ErrPreviewEmpty is returned when the preview payload is empty.
	ErrPreviewEmpty = errors.New("preview image is empty")
	// ErrPreviewTooLarge is returned when the preview exceeds MaxPreviewBytes.
	ErrPreviewTooLarge = errors.New("preview image exceeds maximum size")
	// ErrPreviewNotWebP is returned when the preview does not have a RIFF WEBP header.
	ErrPreviewNotWebP = errors.New("preview image is not a WebP file")
	// ErrPreviewMalformed is returned when the WebP container or bitstream header is invalid.
	ErrPreviewMalformed = errors.New("preview image is malformed")
	// ErrPreviewDimensions is returned when the WebP dimensions are out of the allowed 1..200 range.
	ErrPreviewDimensions = errors.New("preview image dimensions exceed maximum")
)

// ValidateWebPPreview validates that data is a well-formed RFC 9649 WebP image
// within MaxPreviewBytes and with dimensions in [1, MaxPreviewWidth] x [1, MaxPreviewHeight].
func ValidateWebPPreview(data []byte) (width, height int, err error) {
	if len(data) == 0 {
		return 0, 0, ErrPreviewEmpty
	}
	if len(data) > MaxPreviewBytes {
		return 0, 0, ErrPreviewTooLarge
	}
	if len(data) < 12 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return 0, 0, ErrPreviewNotWebP
	}

	riffSize := binary.LittleEndian.Uint32(data[4:8])
	riffEnd := 8 + uint64(riffSize)
	if riffSize < 4 || riffEnd != uint64(len(data)) {
		return 0, 0, ErrPreviewMalformed
	}

	if uint64(12+8) > riffEnd {
		return 0, 0, ErrPreviewMalformed
	}
	fourCC := string(data[12:16])
	chunkSize := uint64(binary.LittleEndian.Uint32(data[16:20]))
	if uint64(20)+chunkSize > riffEnd {
		return 0, 0, ErrPreviewMalformed
	}

	switch fourCC {
	case "VP8 ":
		if _, _, err = parseVP8Dimensions(data[20 : 20+chunkSize]); err != nil {
			return 0, 0, err
		}
		width, height, _ = parseVP8Dimensions(data[20 : 20+chunkSize])
		if tail := uint64(20) + chunkSize; tail != riffEnd && tail+(chunkSize&1) != riffEnd {
			return 0, 0, ErrPreviewMalformed
		}

	case "VP8L":
		if _, _, err = parseVP8LDimensions(data[20 : 20+chunkSize]); err != nil {
			return 0, 0, err
		}
		width, height, _ = parseVP8LDimensions(data[20 : 20+chunkSize])
		if tail := uint64(20) + chunkSize; tail != riffEnd && tail+(chunkSize&1) != riffEnd {
			return 0, 0, ErrPreviewMalformed
		}

	case "VP8X":
		if chunkSize != 10 {
			return 0, 0, ErrPreviewMalformed
		}
		flags := data[20]
		isAnimated := flags&0x02 != 0
		width = int(uint32(data[24])|uint32(data[25])<<8|uint32(data[26])<<16) + 1
		height = int(uint32(data[27])|uint32(data[28])<<8|uint32(data[29])<<16) + 1

		var hasVP8OrVP8L, hasANIM, hasANMF bool
		offset := uint64(20) + chunkSize
		for offset < riffEnd {
			if offset+8 > riffEnd {
				return 0, 0, ErrPreviewMalformed
			}
			subFourCC := string(data[offset : offset+4])
			subSize := uint64(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
			payloadStart := offset + 8
			payloadEnd := payloadStart + subSize
			if payloadEnd > riffEnd {
				return 0, 0, ErrPreviewMalformed
			}

			switch subFourCC {
			case "VP8 ":
				if _, _, err := parseVP8Dimensions(data[payloadStart:payloadEnd]); err != nil {
					return 0, 0, err
				}
				hasVP8OrVP8L = true
			case "VP8L":
				if _, _, err := parseVP8LDimensions(data[payloadStart:payloadEnd]); err != nil {
					return 0, 0, err
				}
				hasVP8OrVP8L = true
			case "ANIM":
				if subSize < 6 {
					return 0, 0, ErrPreviewMalformed
				}
				hasANIM = true
			case "ANMF":
				if subSize < 16 {
					return 0, 0, ErrPreviewMalformed
				}
				hasANMF = true
			}

			pad := subSize & 1
			if payloadEnd == riffEnd {
				offset = payloadEnd
			} else {
				if payloadEnd+pad > riffEnd {
					return 0, 0, ErrPreviewMalformed
				}
				offset = payloadEnd + pad
			}
		}

		if isAnimated {
			if !hasANIM || !hasANMF {
				return 0, 0, ErrPreviewMalformed
			}
		} else if !hasVP8OrVP8L {
			return 0, 0, ErrPreviewMalformed
		}

	default:
		return 0, 0, ErrPreviewMalformed
	}

	if width < 1 || height < 1 || width > MaxPreviewWidth || height > MaxPreviewHeight {
		return 0, 0, ErrPreviewDimensions
	}
	return width, height, nil
}

func parseVP8Dimensions(payload []byte) (int, int, error) {
	if len(payload) < 10 {
		return 0, 0, ErrPreviewMalformed
	}
	// Bit 0 of the 24-bit frame tag must be 0 for a keyframe, followed by the 3-byte start code 0x9D 0x01 0x2A.
	if payload[0]&1 != 0 || payload[3] != 0x9D || payload[4] != 0x01 || payload[5] != 0x2A {
		return 0, 0, ErrPreviewMalformed
	}
	w := int(binary.LittleEndian.Uint16(payload[6:8]) & 0x3FFF)
	h := int(binary.LittleEndian.Uint16(payload[8:10]) & 0x3FFF)
	return w, h, nil
}

func parseVP8LDimensions(payload []byte) (int, int, error) {
	if len(payload) < 5 {
		return 0, 0, ErrPreviewMalformed
	}
	if payload[0] != 0x2F {
		return 0, 0, ErrPreviewMalformed
	}
	bits := binary.LittleEndian.Uint32(payload[1:5])
	if (bits>>29)&0x7 != 0 {
		return 0, 0, ErrPreviewMalformed
	}
	w := int(bits&0x3FFF) + 1
	h := int((bits>>14)&0x3FFF) + 1
	return w, h, nil
}

// ReadAndValidatePreview reads up to MaxPreviewBytes+1 bytes from r and validates
// that the payload is a valid WebP preview image.
func ReadAndValidatePreview(r io.Reader) ([]byte, int, int, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxPreviewBytes+1))
	if err != nil {
		return nil, 0, 0, fmt.Errorf("failed to read preview: %w", err)
	}
	if len(data) == 0 {
		return nil, 0, 0, ErrPreviewEmpty
	}
	if len(data) > MaxPreviewBytes {
		return nil, 0, 0, ErrPreviewTooLarge
	}
	w, h, err := ValidateWebPPreview(data)
	if err != nil {
		return nil, 0, 0, err
	}
	return data, w, h, nil
}
