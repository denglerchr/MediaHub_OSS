package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

type riffChunk struct {
	fourCC string
	data   []byte
}

func buildWebP(chunks ...riffChunk) []byte {
	var body bytes.Buffer
	body.WriteString("WEBP")
	for _, c := range chunks {
		body.WriteString(c.fourCC)
		var sz [4]byte
		binary.LittleEndian.PutUint32(sz[:], uint32(len(c.data)))
		body.Write(sz[:])
		body.Write(c.data)
		if len(c.data)%2 == 1 {
			body.WriteByte(0)
		}
	}

	var out bytes.Buffer
	out.WriteString("RIFF")
	var riffSize [4]byte
	binary.LittleEndian.PutUint32(riffSize[:], uint32(body.Len()))
	out.Write(riffSize[:])
	out.Write(body.Bytes())
	return out.Bytes()
}

func makeVP8Payload(width, height int, keyframe bool, badStartCode bool, extraLen int) []byte {
	payload := make([]byte, 10+extraLen)
	if !keyframe {
		payload[0] = 0x01
	}
	if badStartCode {
		payload[3], payload[4], payload[5] = 0x00, 0x01, 0x2A
	} else {
		payload[3], payload[4], payload[5] = 0x9D, 0x01, 0x2A
	}
	binary.LittleEndian.PutUint16(payload[6:8], uint16(width&0x3FFF))
	binary.LittleEndian.PutUint16(payload[8:10], uint16(height&0x3FFF))
	return payload
}

func makeVP8LPayload(width, height int, badSig bool, version uint32, extraLen int) []byte {
	payload := make([]byte, 5+extraLen)
	if badSig {
		payload[0] = 0x2E
	} else {
		payload[0] = 0x2F
	}
	bits := uint32(width-1)&0x3FFF | (uint32(height-1)&0x3FFF)<<14 | (version&0x7)<<29
	binary.LittleEndian.PutUint32(payload[1:5], bits)
	return payload
}

func makeVP8XPayload(width, height int, flags byte) []byte {
	payload := make([]byte, 10)
	payload[0] = flags
	w := uint32(width - 1)
	h := uint32(height - 1)
	payload[4] = byte(w)
	payload[5] = byte(w >> 8)
	payload[6] = byte(w >> 16)
	payload[7] = byte(h)
	payload[8] = byte(h >> 8)
	payload[9] = byte(h >> 16)
	return payload
}

func TestValidateWebPPreview_ValidFormats(t *testing.T) {
	tests := []struct {
		name   string
		data   []byte
		wantW  int
		wantH  int
	}{
		{
			name:  "valid VP8 lossy",
			data:  buildWebP(riffChunk{fourCC: "VP8 ", data: makeVP8Payload(120, 90, true, false, 0)}),
			wantW: 120,
			wantH: 90,
		},
		{
			name:  "valid VP8L lossless",
			data:  buildWebP(riffChunk{fourCC: "VP8L", data: makeVP8LPayload(64, 48, false, 0, 0)}),
			wantW: 64,
			wantH: 48,
		},
		{
			name: "valid static VP8X with odd-sized subchunk",
			data: buildWebP(
				riffChunk{fourCC: "VP8X", data: makeVP8XPayload(160, 100, 0x00)},
				riffChunk{fourCC: "ICCP", data: []byte{1, 2, 3}},
				riffChunk{fourCC: "VP8L", data: makeVP8LPayload(160, 100, false, 0, 0)},
			),
			wantW: 160,
			wantH: 100,
		},
		{
			name: "valid animated VP8X",
			data: buildWebP(
				riffChunk{fourCC: "VP8X", data: makeVP8XPayload(180, 150, 0x02)},
				riffChunk{fourCC: "ANIM", data: make([]byte, 6)},
				riffChunk{fourCC: "ANMF", data: make([]byte, 24)},
			),
			wantW: 180,
			wantH: 150,
		},
		{
			name:  "exact 200x200 boundary",
			data:  buildWebP(riffChunk{fourCC: "VP8 ", data: makeVP8Payload(200, 200, true, false, 0)}),
			wantW: 200,
			wantH: 200,
		},
		{
			name:  "exact 1x1 boundary",
			data:  buildWebP(riffChunk{fourCC: "VP8L", data: makeVP8LPayload(1, 1, false, 0, 0)}),
			wantW: 1,
			wantH: 1,
		},
		{
			name: "exact 128 KiB boundary",
			data: func() []byte {
				// 12 (RIFF header) + 8 (VP8 chunk header) = 20 bytes overhead.
				payload := makeVP8Payload(200, 200, true, false, MaxPreviewBytes-20-10)
				d := buildWebP(riffChunk{fourCC: "VP8 ", data: payload})
				if len(d) != MaxPreviewBytes {
					t.Fatalf("test fixture length = %d, want %d", len(d), MaxPreviewBytes)
				}
				return d
			}(),
			wantW: 200,
			wantH: 200,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w, h, err := ValidateWebPPreview(tc.data)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if w != tc.wantW || h != tc.wantH {
				t.Fatalf("got dimensions %dx%d, want %dx%d", w, h, tc.wantW, tc.wantH)
			}
		})
	}
}

func TestValidateWebPPreview_Errors(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		wantErr error
	}{
		{
			name:    "empty",
			data:    nil,
			wantErr: ErrPreviewEmpty,
		},
		{
			name:    "128 KiB + 1",
			data:    make([]byte, MaxPreviewBytes+1),
			wantErr: ErrPreviewTooLarge,
		},
		{
			name:    "PNG header",
			data:    []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"),
			wantErr: ErrPreviewNotWebP,
		},
		{
			name:    "JPEG header",
			data:    []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00\x01\x01"),
			wantErr: ErrPreviewNotWebP,
		},
		{
			name:    "plain text",
			data:    []byte("not a webp image at all"),
			wantErr: ErrPreviewNotWebP,
		},
		{
			name:    "too short for RIFF WEBP",
			data:    []byte("RIFF\x04\x00\x00\x00WEB"),
			wantErr: ErrPreviewNotWebP,
		},
		{
			name: "truncated RIFF size beyond data",
			data: func() []byte {
				d := buildWebP(riffChunk{fourCC: "VP8 ", data: makeVP8Payload(100, 100, true, false, 0)})
				binary.LittleEndian.PutUint32(d[4:8], uint32(len(d)+10))
				return d
			}(),
			wantErr: ErrPreviewMalformed,
		},
		{
			name:    "RIFF size smaller than 4",
			data:    []byte("RIFF\x03\x00\x00\x00WEBP"),
			wantErr: ErrPreviewMalformed,
		},
		{
			name:    "missing first chunk header",
			data:    []byte("RIFF\x08\x00\x00\x00WEBP1234"),
			wantErr: ErrPreviewMalformed,
		},
		{
			name:    "bad VP8 start code",
			data:    buildWebP(riffChunk{fourCC: "VP8 ", data: makeVP8Payload(100, 100, true, true, 0)}),
			wantErr: ErrPreviewMalformed,
		},
		{
			name:    "interframe VP8",
			data:    buildWebP(riffChunk{fourCC: "VP8 ", data: makeVP8Payload(100, 100, false, false, 0)}),
			wantErr: ErrPreviewMalformed,
		},
		{
			name:    "truncated VP8 chunk",
			data:    buildWebP(riffChunk{fourCC: "VP8 ", data: make([]byte, 9)}),
			wantErr: ErrPreviewMalformed,
		},
		{
			name:    "bad VP8L signature",
			data:    buildWebP(riffChunk{fourCC: "VP8L", data: makeVP8LPayload(100, 100, true, 0, 0)}),
			wantErr: ErrPreviewMalformed,
		},
		{
			name:    "bad VP8L version",
			data:    buildWebP(riffChunk{fourCC: "VP8L", data: makeVP8LPayload(100, 100, false, 1, 0)}),
			wantErr: ErrPreviewMalformed,
		},
		{
			name:    "truncated VP8L chunk",
			data:    buildWebP(riffChunk{fourCC: "VP8L", data: make([]byte, 4)}),
			wantErr: ErrPreviewMalformed,
		},
		{
			name:    "VP8X without image chunks",
			data:    buildWebP(riffChunk{fourCC: "VP8X", data: makeVP8XPayload(100, 100, 0x00)}),
			wantErr: ErrPreviewMalformed,
		},
		{
			name: "VP8X with wrong chunk size",
			data: buildWebP(
				riffChunk{fourCC: "VP8X", data: make([]byte, 8)},
				riffChunk{fourCC: "VP8 ", data: makeVP8Payload(100, 100, true, false, 0)},
			),
			wantErr: ErrPreviewMalformed,
		},
		{
			name: "animated VP8X without ANIM",
			data: buildWebP(
				riffChunk{fourCC: "VP8X", data: makeVP8XPayload(100, 100, 0x02)},
				riffChunk{fourCC: "ANMF", data: make([]byte, 24)},
			),
			wantErr: ErrPreviewMalformed,
		},
		{
			name: "animated VP8X without ANMF",
			data: buildWebP(
				riffChunk{fourCC: "VP8X", data: makeVP8XPayload(100, 100, 0x02)},
				riffChunk{fourCC: "ANIM", data: make([]byte, 6)},
			),
			wantErr: ErrPreviewMalformed,
		},
		{
			name:    "unknown fourCC",
			data:    buildWebP(riffChunk{fourCC: "JUNK", data: make([]byte, 12)}),
			wantErr: ErrPreviewMalformed,
		},
		{
			name:    "width 201x200",
			data:    buildWebP(riffChunk{fourCC: "VP8 ", data: makeVP8Payload(201, 200, true, false, 0)}),
			wantErr: ErrPreviewDimensions,
		},
		{
			name:    "height 200x201",
			data:    buildWebP(riffChunk{fourCC: "VP8L", data: makeVP8LPayload(200, 201, false, 0, 0)}),
			wantErr: ErrPreviewDimensions,
		},
		{
			name:    "VP8X 201x200",
			data:    buildWebP(riffChunk{fourCC: "VP8X", data: makeVP8XPayload(201, 200, 0)}, riffChunk{fourCC: "VP8 ", data: makeVP8Payload(201, 200, true, false, 0)}),
			wantErr: ErrPreviewDimensions,
		},
		{
			name:    "VP8 0x10 dimensions",
			data:    buildWebP(riffChunk{fourCC: "VP8 ", data: makeVP8Payload(0, 10, true, false, 0)}),
			wantErr: ErrPreviewDimensions,
		},
		{
			name:    "VP8L 16384x16384 dimensions",
			data:    buildWebP(riffChunk{fourCC: "VP8L", data: makeVP8LPayload(16384, 16384, false, 0, 0)}),
			wantErr: ErrPreviewDimensions,
		},
		{
			name: "VP8X truncated ANIM subchunk",
			data: buildWebP(
				riffChunk{fourCC: "VP8X", data: makeVP8XPayload(100, 100, 0x02)},
				riffChunk{fourCC: "ANIM", data: make([]byte, 4)},
				riffChunk{fourCC: "ANMF", data: make([]byte, 24)},
			),
			wantErr: ErrPreviewMalformed,
		},
		{
			name: "VP8X truncated ANMF subchunk",
			data: buildWebP(
				riffChunk{fourCC: "VP8X", data: makeVP8XPayload(100, 100, 0x02)},
				riffChunk{fourCC: "ANIM", data: make([]byte, 6)},
				riffChunk{fourCC: "ANMF", data: make([]byte, 12)},
			),
			wantErr: ErrPreviewMalformed,
		},
		{
			name: "VP8X malformed embedded VP8 subchunk",
			data: buildWebP(
				riffChunk{fourCC: "VP8X", data: makeVP8XPayload(100, 100, 0x00)},
				riffChunk{fourCC: "VP8 ", data: makeVP8Payload(100, 100, true, true, 0)},
			),
			wantErr: ErrPreviewMalformed,
		},
		{
			name: "VP8X malformed embedded VP8L subchunk",
			data: buildWebP(
				riffChunk{fourCC: "VP8X", data: makeVP8XPayload(100, 100, 0x00)},
				riffChunk{fourCC: "VP8L", data: makeVP8LPayload(100, 100, true, 0, 0)},
			),
			wantErr: ErrPreviewMalformed,
		},
		{
			name: "trailing garbage bytes past RIFF end",
			data: append(
				buildWebP(riffChunk{fourCC: "VP8 ", data: makeVP8Payload(100, 100, true, false, 0)}),
				0xDE, 0xAD, 0xBE, 0xEF,
			),
			wantErr: ErrPreviewMalformed,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := ValidateWebPPreview(tc.data)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("got err %v, want %v", err, tc.wantErr)
			}
		})
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) {
	return 0, errors.New("read boom")
}

func TestReadAndValidatePreview(t *testing.T) {
	valid := buildWebP(riffChunk{fourCC: "VP8L", data: makeVP8LPayload(128, 64, false, 0, 0)})

	got, w, h, err := ReadAndValidatePreview(bytes.NewReader(valid))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w != 128 || h != 64 {
		t.Fatalf("got %dx%d, want 128x64", w, h)
	}
	if !bytes.Equal(got, valid) {
		t.Fatal("returned bytes do not match input")
	}

	if _, _, _, err := ReadAndValidatePreview(bytes.NewReader(nil)); !errors.Is(err, ErrPreviewEmpty) {
		t.Fatalf("expected ErrPreviewEmpty, got %v", err)
	}

	oversized := io.LimitReader(bytes.NewReader(make([]byte, MaxPreviewBytes+10)), MaxPreviewBytes+10)
	if _, _, _, err := ReadAndValidatePreview(oversized); !errors.Is(err, ErrPreviewTooLarge) {
		t.Fatalf("expected ErrPreviewTooLarge, got %v", err)
	}

	if _, _, _, err := ReadAndValidatePreview(errReader{}); err == nil {
		t.Fatal("expected error from failing reader, got nil")
	}
}
