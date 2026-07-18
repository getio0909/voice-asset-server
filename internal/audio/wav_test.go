package audio

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"testing"
)

func TestProbeWAVPCM(t *testing.T) {
	wav := wavFixture(t, basicFormat(1, 2, 48_000, 16), wavChunk{id: "JUNK", data: []byte{1}}, wavChunk{id: "data", size: 192_000})

	got, err := ProbeWAV(bytes.NewReader(wav), int64(len(wav)))
	if err != nil {
		t.Fatalf("ProbeWAV() error = %v", err)
	}
	want := Metadata{
		Container:  "wav",
		Codec:      "pcm_s16le",
		SampleRate: 48_000,
		Channels:   2,
		Bitrate:    1_536_000,
		DurationMS: 1_000,
		DataBytes:  192_000,
	}
	if got != want {
		t.Fatalf("ProbeWAV() = %+v, want %+v", got, want)
	}
}

func TestProbeWAVIEEEFloat(t *testing.T) {
	wav := wavFixture(t, basicFormat(3, 1, 44_100, 32), wavChunk{id: "data", size: 176_400})

	got, err := ProbeWAV(bytes.NewReader(wav), int64(len(wav)))
	if err != nil {
		t.Fatalf("ProbeWAV() error = %v", err)
	}
	if got.Codec != "pcm_f32le" || got.DurationMS != 1_000 || got.Bitrate != 1_411_200 {
		t.Fatalf("ProbeWAV() = %+v", got)
	}
}

func TestProbeWAVFile(t *testing.T) {
	wav := wavFixture(t, basicFormat(1, 1, 16_000, 8), wavChunk{id: "data", size: 16_000})
	file, err := os.CreateTemp(t.TempDir(), "audio-*.wav")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.Write(wav); err != nil {
		t.Fatal(err)
	}
	before, err := file.Seek(7, 0)
	if err != nil {
		t.Fatal(err)
	}

	got, err := ProbeWAVFile(file)
	if err != nil {
		t.Fatalf("ProbeWAVFile() error = %v", err)
	}
	if got.Codec != "pcm_u8" || got.DataBytes != 16_000 {
		t.Fatalf("ProbeWAVFile() = %+v", got)
	}
	after, err := file.Seek(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("ProbeWAVFile() moved file position from %d to %d", before, after)
	}
}

func TestProbeWAVAcceptsDataBeforeFormat(t *testing.T) {
	wav := wavFixture(t, nil,
		wavChunk{id: "data", size: 16_000},
		wavChunk{id: "fmt ", data: basicFormat(1, 1, 8_000, 16)},
	)

	got, err := ProbeWAV(bytes.NewReader(wav), int64(len(wav)))
	if err != nil {
		t.Fatalf("ProbeWAV() error = %v", err)
	}
	if got.DurationMS != 1_000 {
		t.Fatalf("ProbeWAV() duration = %d, want 1000", got.DurationMS)
	}
}

func TestProbeWAVRejectsMalformedInput(t *testing.T) {
	valid := wavFixture(t, basicFormat(1, 1, 8_000, 16), wavChunk{id: "data", size: 16_000})
	withoutData := wavFixture(t, basicFormat(1, 1, 8_000, 16))
	withoutFormat := wavFixture(t, nil, wavChunk{id: "data", size: 16_000})
	truncatedChunk := append([]byte(nil), valid...)
	binary.LittleEndian.PutUint32(truncatedChunk[16:20], math.MaxUint32)
	missingOddPadding := wavFixture(t, basicFormat(1, 1, 8_000, 16), wavChunk{id: "JUNK", data: []byte{1}})
	missingOddPadding = missingOddPadding[:len(missingOddPadding)-1]
	binary.LittleEndian.PutUint32(missingOddPadding[4:8], uint32(len(missingOddPadding)-8))
	duplicateFormat := wavFixture(t, basicFormat(1, 1, 8_000, 16),
		wavChunk{id: "fmt ", data: basicFormat(1, 1, 8_000, 16)},
		wavChunk{id: "data", size: 16_000},
	)
	duplicateData := wavFixture(t, basicFormat(1, 1, 8_000, 16),
		wavChunk{id: "data", size: 8_000},
		wavChunk{id: "data", size: 8_000},
	)
	trailingPayload := append(append([]byte(nil), valid...), []byte("trailing")...)

	tests := []struct {
		name string
		data []byte
		size int64
	}{
		{name: "negative size", data: valid, size: -1},
		{name: "short header", data: []byte("RIFF"), size: 4},
		{name: "not RIFF", data: replaceBytes(valid, 0, "NOPE"), size: int64(len(valid))},
		{name: "not WAVE", data: replaceBytes(valid, 8, "AVI "), size: int64(len(valid))},
		{name: "declared RIFF exceeds input", data: replaceUint32(valid, 4, uint32(len(valid))), size: int64(len(valid))},
		{name: "trailing payload", data: trailingPayload, size: int64(len(trailingPayload))},
		{name: "declared RIFF too small", data: replaceUint32(valid, 4, 3), size: int64(len(valid))},
		{name: "chunk exceeds RIFF", data: truncatedChunk, size: int64(len(truncatedChunk))},
		{name: "odd chunk missing padding", data: missingOddPadding, size: int64(len(missingOddPadding))},
		{name: "duplicate fmt", data: duplicateFormat, size: int64(len(duplicateFormat))},
		{name: "duplicate data", data: duplicateData, size: int64(len(duplicateData))},
		{name: "missing fmt", data: withoutFormat, size: int64(len(withoutFormat))},
		{name: "missing data", data: withoutData, size: int64(len(withoutData))},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ProbeWAV(bytes.NewReader(test.data), test.size)
			if !errors.Is(err, ErrInvalidWAV) {
				t.Fatalf("ProbeWAV() error = %v, want ErrInvalidWAV", err)
			}
		})
	}
}

func TestProbeWAVRejectsInvalidFormat(t *testing.T) {
	tests := []struct {
		name   string
		format []byte
	}{
		{name: "short fmt", format: make([]byte, 15)},
		{name: "unsupported format", format: basicFormat(6, 1, 8_000, 8)},
		{name: "zero channels", format: basicFormat(1, 0, 8_000, 16)},
		{name: "too many channels", format: basicFormat(1, 65, 8_000, 16)},
		{name: "zero sample rate", format: basicFormat(1, 1, 0, 16)},
		{name: "implausibly low sample rate", format: basicFormat(1, 1, 999, 16)},
		{name: "excessive sample rate", format: basicFormat(1, 1, 768_001, 16)},
		{name: "invalid PCM bits", format: basicFormat(1, 1, 8_000, 12)},
		{name: "invalid float bits", format: basicFormat(3, 1, 8_000, 16)},
		{name: "inconsistent block align", format: mutateFormat(basicFormat(1, 2, 8_000, 16), 12, 1, 2)},
		{name: "inconsistent byte rate", format: mutateFormat(basicFormat(1, 1, 8_000, 16), 8, 1, 4)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wav := wavFixture(t, test.format, wavChunk{id: "data", size: 16_000})
			_, err := ProbeWAV(bytes.NewReader(wav), int64(len(wav)))
			if !errors.Is(err, ErrInvalidWAV) {
				t.Fatalf("ProbeWAV() error = %v, want ErrInvalidWAV", err)
			}
		})
	}
}

func TestProbeWAVRejectsUnalignedAudioData(t *testing.T) {
	wav := wavFixture(t, basicFormat(1, 2, 48_000, 16), wavChunk{id: "data", size: 3})

	_, err := ProbeWAV(bytes.NewReader(wav), int64(len(wav)))
	if !errors.Is(err, ErrInvalidWAV) {
		t.Fatalf("ProbeWAV() error = %v, want ErrInvalidWAV", err)
	}
}

func FuzzProbeWAV(f *testing.F) {
	f.Add(wavFixture(f, basicFormat(1, 1, 8_000, 16), wavChunk{id: "data", size: 16_000}))
	f.Add([]byte("RIFF\x04\x00\x00\x00WAVE"))

	f.Fuzz(func(t *testing.T, data []byte) {
		metadata, err := ProbeWAV(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return
		}
		if metadata.Container != "wav" || metadata.SampleRate == 0 || metadata.Channels == 0 || metadata.Bitrate == 0 {
			t.Fatalf("ProbeWAV() returned invalid metadata: %+v", metadata)
		}
	})
}

type testFataler interface {
	Helper()
	Fatalf(string, ...any)
}

type wavChunk struct {
	id   string
	data []byte
	size uint32
}

func wavFixture(t testFataler, format []byte, chunks ...wavChunk) []byte {
	t.Helper()
	var body bytes.Buffer
	body.WriteString("WAVE")
	if format != nil {
		writeChunk(t, &body, wavChunk{id: "fmt ", data: format})
	}
	for _, chunk := range chunks {
		writeChunk(t, &body, chunk)
	}
	var result bytes.Buffer
	result.WriteString("RIFF")
	if err := binary.Write(&result, binary.LittleEndian, uint32(body.Len())); err != nil {
		t.Fatalf("write RIFF size: %v", err)
	}
	result.Write(body.Bytes())
	return result.Bytes()
}

func writeChunk(t testFataler, dst *bytes.Buffer, chunk wavChunk) {
	t.Helper()
	if len(chunk.id) != 4 {
		t.Fatalf("chunk ID %q must be four bytes", chunk.id)
	}
	dst.WriteString(chunk.id)
	size := chunk.size
	if chunk.data != nil {
		size = uint32(len(chunk.data))
	}
	if err := binary.Write(dst, binary.LittleEndian, size); err != nil {
		t.Fatalf("write chunk size: %v", err)
	}
	if chunk.data != nil {
		dst.Write(chunk.data)
	} else {
		dst.Grow(int(size))
		dst.Write(make([]byte, size))
	}
	if size%2 != 0 {
		dst.WriteByte(0)
	}
}

func basicFormat(format, channels uint16, sampleRate uint32, bits uint16) []byte {
	result := make([]byte, 16)
	binary.LittleEndian.PutUint16(result[0:2], format)
	binary.LittleEndian.PutUint16(result[2:4], channels)
	binary.LittleEndian.PutUint32(result[4:8], sampleRate)
	blockAlign := uint32(channels) * uint32(bits) / 8
	binary.LittleEndian.PutUint32(result[8:12], sampleRate*blockAlign)
	binary.LittleEndian.PutUint16(result[12:14], uint16(blockAlign))
	binary.LittleEndian.PutUint16(result[14:16], bits)
	return result
}

func mutateFormat(input []byte, offset, value, width int) []byte {
	result := append([]byte(nil), input...)
	switch width {
	case 2:
		binary.LittleEndian.PutUint16(result[offset:offset+width], uint16(value))
	case 4:
		binary.LittleEndian.PutUint32(result[offset:offset+width], uint32(value))
	}
	return result
}

func replaceBytes(input []byte, offset int, replacement string) []byte {
	result := append([]byte(nil), input...)
	copy(result[offset:], replacement)
	return result
}

func replaceUint32(input []byte, offset int, replacement uint32) []byte {
	result := append([]byte(nil), input...)
	binary.LittleEndian.PutUint32(result[offset:offset+4], replacement)
	return result
}
