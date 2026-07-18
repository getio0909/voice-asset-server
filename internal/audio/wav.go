// Package audio safely extracts metadata from supported audio containers.
package audio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	wavHeaderSize   = int64(12)
	chunkHeaderSize = int64(8)

	// The limits cover ordinary and high-resolution audio while rejecting
	// values that are not useful for this service.
	minWAVSampleRate = uint32(1_000)
	maxWAVSampleRate = uint32(768_000)
	maxWAVChannels   = uint16(64)
)

// ErrInvalidWAV identifies malformed, truncated, ambiguous, or unsupported
// WAV input. Callers can use errors.Is to distinguish it from file I/O errors.
var ErrInvalidWAV = errors.New("invalid WAV")

// Metadata is the normalized metadata extracted from a WAV file.
type Metadata struct {
	Container  string `json:"container"`
	Codec      string `json:"codec"`
	SampleRate uint32 `json:"sample_rate"`
	Channels   uint16 `json:"channels"`
	Bitrate    uint64 `json:"bitrate"`
	DurationMS int64  `json:"duration_ms"`
	DataBytes  int64  `json:"data_bytes"`
}

// ProbeWAV parses a RIFF/WAVE stream without reading audio payloads into
// memory. size must be the total number of bytes available through reader.
func ProbeWAV(reader io.ReaderAt, size int64) (Metadata, error) {
	if reader == nil {
		return Metadata{}, invalidWAV("reader is nil")
	}
	if size < wavHeaderSize {
		return Metadata{}, invalidWAV("file is shorter than the RIFF header")
	}

	var header [wavHeaderSize]byte
	if err := readAt(reader, header[:], 0); err != nil {
		return Metadata{}, invalidWAV("read RIFF header: %v", err)
	}
	if string(header[0:4]) != "RIFF" {
		return Metadata{}, invalidWAV("missing RIFF signature")
	}
	if string(header[8:12]) != "WAVE" {
		return Metadata{}, invalidWAV("missing WAVE form type")
	}

	declaredEnd := uint64(binary.LittleEndian.Uint32(header[4:8])) + 8
	if declaredEnd < uint64(wavHeaderSize) {
		return Metadata{}, invalidWAV("RIFF declaration is shorter than its header")
	}
	if declaredEnd != uint64(size) {
		return Metadata{}, invalidWAV("RIFF declaration does not match the available input")
	}

	var (
		format    wavFormat
		foundFmt  bool
		dataBytes int64
		foundData bool
	)
	for offset := uint64(wavHeaderSize); offset < declaredEnd; {
		if declaredEnd-offset < uint64(chunkHeaderSize) {
			return Metadata{}, invalidWAV("truncated chunk header at offset %d", offset)
		}

		var chunkHeader [chunkHeaderSize]byte
		if err := readAt(reader, chunkHeader[:], int64(offset)); err != nil {
			return Metadata{}, invalidWAV("read chunk header at offset %d: %v", offset, err)
		}
		chunkID := string(chunkHeader[0:4])
		chunkSize := uint64(binary.LittleEndian.Uint32(chunkHeader[4:8]))
		dataStart := offset + uint64(chunkHeaderSize)
		dataEnd := dataStart + chunkSize
		if dataEnd < dataStart || dataEnd > declaredEnd {
			return Metadata{}, invalidWAV("chunk %q exceeds its RIFF boundary", chunkID)
		}
		paddedEnd := dataEnd + chunkSize%2
		if paddedEnd < dataEnd || paddedEnd > declaredEnd {
			return Metadata{}, invalidWAV("chunk %q is missing its alignment byte", chunkID)
		}

		switch chunkID {
		case "fmt ":
			if foundFmt {
				return Metadata{}, invalidWAV("duplicate fmt chunk")
			}
			if chunkSize < 16 {
				return Metadata{}, invalidWAV("fmt chunk is shorter than 16 bytes")
			}
			var formatBytes [16]byte
			if err := readAt(reader, formatBytes[:], int64(dataStart)); err != nil {
				return Metadata{}, invalidWAV("read fmt chunk: %v", err)
			}
			parsed, err := parseWAVFormat(formatBytes[:])
			if err != nil {
				return Metadata{}, err
			}
			format = parsed
			foundFmt = true
		case "data":
			if foundData {
				return Metadata{}, invalidWAV("duplicate data chunk")
			}
			dataBytes = int64(chunkSize)
			foundData = true
		}

		offset = paddedEnd
	}

	if !foundFmt {
		return Metadata{}, invalidWAV("missing fmt chunk")
	}
	if !foundData {
		return Metadata{}, invalidWAV("missing data chunk")
	}
	if dataBytes%int64(format.blockAlign) != 0 {
		return Metadata{}, invalidWAV("data chunk does not contain complete audio frames")
	}

	frameCount := dataBytes / int64(format.blockAlign)
	durationMS := frameCount/int64(format.sampleRate)*1_000 +
		(frameCount%int64(format.sampleRate))*1_000/int64(format.sampleRate)
	return Metadata{
		Container:  "wav",
		Codec:      format.codec,
		SampleRate: format.sampleRate,
		Channels:   format.channels,
		Bitrate:    uint64(format.byteRate) * 8,
		DurationMS: durationMS,
		DataBytes:  dataBytes,
	}, nil
}

// ProbeWAVFile extracts metadata from an already-open regular file. ReaderAt
// access means the file's current seek position is not changed.
func ProbeWAVFile(file ProbeSource) (Metadata, error) {
	if file == nil {
		return Metadata{}, invalidWAV("file is nil")
	}
	info, err := file.Stat()
	if err != nil {
		return Metadata{}, fmt.Errorf("stat WAV file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return Metadata{}, invalidWAV("input is not a regular file")
	}
	return ProbeWAV(file, info.Size())
}

type wavFormat struct {
	codec      string
	channels   uint16
	sampleRate uint32
	byteRate   uint32
	blockAlign uint16
}

func parseWAVFormat(data []byte) (wavFormat, error) {
	formatTag := binary.LittleEndian.Uint16(data[0:2])
	channels := binary.LittleEndian.Uint16(data[2:4])
	sampleRate := binary.LittleEndian.Uint32(data[4:8])
	byteRate := binary.LittleEndian.Uint32(data[8:12])
	blockAlign := binary.LittleEndian.Uint16(data[12:14])
	bitsPerSample := binary.LittleEndian.Uint16(data[14:16])

	if channels == 0 || channels > maxWAVChannels {
		return wavFormat{}, invalidWAV("channel count %d is outside 1..%d", channels, maxWAVChannels)
	}
	if sampleRate < minWAVSampleRate || sampleRate > maxWAVSampleRate {
		return wavFormat{}, invalidWAV("sample rate %d is outside %d..%d", sampleRate, minWAVSampleRate, maxWAVSampleRate)
	}

	codec, validBits := wavCodec(formatTag, bitsPerSample)
	if !validBits {
		return wavFormat{}, invalidWAV("unsupported format tag %d with %d bits per sample", formatTag, bitsPerSample)
	}
	expectedBlockAlign := uint32(channels) * uint32(bitsPerSample) / 8
	if expectedBlockAlign == 0 || expectedBlockAlign > uint32(^uint16(0)) || uint32(blockAlign) != expectedBlockAlign {
		return wavFormat{}, invalidWAV("block align %d is inconsistent with the format", blockAlign)
	}
	expectedByteRate := uint64(sampleRate) * uint64(expectedBlockAlign)
	if expectedByteRate > uint64(^uint32(0)) || uint64(byteRate) != expectedByteRate {
		return wavFormat{}, invalidWAV("byte rate %d is inconsistent with the format", byteRate)
	}

	return wavFormat{
		codec:      codec,
		channels:   channels,
		sampleRate: sampleRate,
		byteRate:   byteRate,
		blockAlign: blockAlign,
	}, nil
}

func wavCodec(formatTag, bitsPerSample uint16) (string, bool) {
	switch formatTag {
	case 1: // WAVE_FORMAT_PCM
		switch bitsPerSample {
		case 8:
			return "pcm_u8", true
		case 16:
			return "pcm_s16le", true
		case 24:
			return "pcm_s24le", true
		case 32:
			return "pcm_s32le", true
		}
	case 3: // WAVE_FORMAT_IEEE_FLOAT
		switch bitsPerSample {
		case 32:
			return "pcm_f32le", true
		case 64:
			return "pcm_f64le", true
		}
	}
	return "", false
}

func readAt(reader io.ReaderAt, destination []byte, offset int64) error {
	read, err := reader.ReadAt(destination, offset)
	if read == len(destination) {
		return nil
	}
	if err == nil {
		err = io.ErrUnexpectedEOF
	}
	return err
}

func invalidWAV(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidWAV, fmt.Sprintf(format, args...))
}
