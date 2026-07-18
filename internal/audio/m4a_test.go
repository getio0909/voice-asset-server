package audio

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"testing"
)

func TestProbeM4AAAC(t *testing.T) {
	m4a := m4aFixture(t, 1, 44_100, 44_100, 16_000)

	got, err := ProbeM4A(bytes.NewReader(m4a), int64(len(m4a)))
	if err != nil {
		t.Fatalf("ProbeM4A() error = %v", err)
	}
	want := Metadata{
		Container: "m4a", Codec: "aac", SampleRate: 44_100, Channels: 1,
		Bitrate: 128_000, DurationMS: 1_000, DataBytes: 16_000,
	}
	if got != want {
		t.Fatalf("ProbeM4A() = %+v, want %+v", got, want)
	}
}

func TestProbeM4AAcceptsMoovAfterMediaData(t *testing.T) {
	m4a := m4aFixture(t, 2, 48_000, 96_000, 24_000)

	got, err := ProbeM4A(bytes.NewReader(m4a), int64(len(m4a)))
	if err != nil {
		t.Fatalf("ProbeM4A() error = %v", err)
	}
	if got.DurationMS != 2_000 || got.SampleRate != 48_000 || got.Channels != 2 {
		t.Fatalf("ProbeM4A() = %+v", got)
	}
}

func TestProbeM4ARejectsMalformedOrUnsupportedInput(t *testing.T) {
	valid := m4aFixture(t, 1, 44_100, 44_100, 64)
	truncated := append([]byte(nil), valid...)
	binary.BigEndian.PutUint32(truncated[0:4], uint32(len(valid)+1))
	noMedia := append(
		box(t, "ftyp", ftypPayload()),
		box(t, "moov", audioTrack(t, 1, 44_100, 44_100, []byte{0x12, 0x08}))...,
	)
	noMovie := append(box(t, "ftyp", ftypPayload()), box(t, "mdat", make([]byte, 64))...)
	nonAudio := append(box(t, "ftyp", ftypPayload()), box(t, "mdat", make([]byte, 64))...)
	nonAudio = append(nonAudio, box(t, "moov", nonAudioTrack(t))...)
	badChannels := m4aFixture(t, 0, 44_100, 44_100, 64)
	badSampleRate := m4aFixture(t, 1, 999, 999, 64)
	unsupportedCodec := m4aFixtureWithAudioConfig(t, 1, 44_100, 44_100, 64, []byte{0x40, 0x00})
	missingDescriptor := m4aFixtureWithAudioConfig(t, 1, 44_100, 44_100, 64, nil)
	mismatchedConfig := m4aFixtureWithAudioConfig(t, 1, 44_100, 44_100, 64, aacLCConfig(48_000, 1))

	tests := []struct {
		name string
		data []byte
		size int64
	}{
		{name: "nil reader", data: nil, size: 0},
		{name: "negative size", data: valid, size: -1},
		{name: "short file", data: []byte("short"), size: 5},
		{name: "truncated box", data: truncated, size: int64(len(truncated))},
		{name: "missing media data", data: noMedia, size: int64(len(noMedia))},
		{name: "missing movie", data: noMovie, size: int64(len(noMovie))},
		{name: "missing audio track", data: nonAudio, size: int64(len(nonAudio))},
		{name: "invalid channels", data: badChannels, size: int64(len(badChannels))},
		{name: "invalid sample rate", data: badSampleRate, size: int64(len(badSampleRate))},
		{name: "unsupported MPEG-4 audio object type", data: unsupportedCodec, size: int64(len(unsupportedCodec))},
		{name: "missing elementary stream descriptor", data: missingDescriptor, size: int64(len(missingDescriptor))},
		{name: "conflicting audio specific config", data: mismatchedConfig, size: int64(len(mismatchedConfig))},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var reader io.ReaderAt
			if test.data != nil {
				reader = bytes.NewReader(test.data)
			}
			_, err := ProbeM4A(reader, test.size)
			if !errors.Is(err, ErrInvalidM4A) {
				t.Fatalf("ProbeM4A() error = %v, want ErrInvalidM4A", err)
			}
		})
	}
}

func TestProbeM4AExternalFixture(t *testing.T) {
	path := os.Getenv("VOICEASSET_M4A_FIXTURE")
	if path == "" {
		t.Skip("VOICEASSET_M4A_FIXTURE is not set")
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open external M4A fixture: %v", err)
	}
	defer file.Close()

	metadata, err := ProbeM4AFile(file)
	if err != nil {
		t.Fatalf("ProbeM4AFile() error = %v", err)
	}
	if metadata.Container != "m4a" || metadata.Codec != "aac" || metadata.DurationMS <= 0 {
		t.Fatalf("ProbeM4AFile() returned invalid metadata: %+v", metadata)
	}
}

func FuzzProbeM4A(f *testing.F) {
	f.Add(m4aFixture(f, 1, 44_100, 44_100, 64))
	f.Add([]byte("\x00\x00\x00\x08free"))

	f.Fuzz(func(t *testing.T, data []byte) {
		metadata, err := ProbeM4A(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return
		}
		if metadata.Container != "m4a" || metadata.Codec != "aac" || metadata.SampleRate == 0 ||
			metadata.Channels == 0 || metadata.DurationMS <= 0 || metadata.DataBytes <= 0 {
			t.Fatalf("ProbeM4A() returned invalid metadata: %+v", metadata)
		}
	})
}

func m4aFixture(t testFataler, channels uint16, sampleRate, duration uint32, mediaBytes int) []byte {
	return m4aFixtureWithAudioConfig(t, channels, sampleRate, duration, mediaBytes, aacLCConfig(sampleRate, channels))
}

func m4aFixtureWithAudioConfig(
	t testFataler,
	channels uint16,
	sampleRate, duration uint32,
	mediaBytes int,
	audioSpecificConfig []byte,
) []byte {
	t.Helper()
	result := box(t, "ftyp", ftypPayload())
	result = append(result, box(t, "mdat", make([]byte, mediaBytes))...)
	result = append(result, box(t, "moov", audioTrack(t, channels, sampleRate, duration, audioSpecificConfig))...)
	return result
}

func ftypPayload() []byte {
	return append([]byte("isom\x00\x00\x02\x00"), []byte("isomiso2mp41M4A ")...)
}

func audioTrack(
	t testFataler,
	channels uint16,
	sampleRate, duration uint32,
	audioSpecificConfig []byte,
) []byte {
	t.Helper()
	mdhd := make([]byte, 24)
	binary.BigEndian.PutUint32(mdhd[12:16], sampleRate)
	binary.BigEndian.PutUint32(mdhd[16:20], duration)
	hdlr := make([]byte, 24)
	copy(hdlr[8:12], "soun")
	sampleEntry := make([]byte, 36)
	copy(sampleEntry[4:8], "mp4a")
	binary.BigEndian.PutUint16(sampleEntry[14:16], 1)
	binary.BigEndian.PutUint16(sampleEntry[24:26], channels)
	binary.BigEndian.PutUint16(sampleEntry[26:28], 16)
	binary.BigEndian.PutUint32(sampleEntry[32:36], sampleRate<<16)
	if audioSpecificConfig != nil {
		sampleEntry = append(sampleEntry, box(t, "esds", esdsPayload(audioSpecificConfig))...)
	}
	binary.BigEndian.PutUint32(sampleEntry[0:4], uint32(len(sampleEntry)))
	stsdPayload := make([]byte, 8)
	binary.BigEndian.PutUint32(stsdPayload[4:8], 1)
	stsdPayload = append(stsdPayload, sampleEntry...)
	stbl := box(t, "stbl", box(t, "stsd", stsdPayload))
	minf := box(t, "minf", stbl)
	mdia := append(box(t, "mdhd", mdhd), box(t, "hdlr", hdlr)...)
	mdia = append(mdia, minf...)
	return box(t, "trak", box(t, "mdia", mdia))
}

func esdsPayload(audioSpecificConfig []byte) []byte {
	decoderSpecific := descriptor(0x05, audioSpecificConfig)
	decoderConfig := make([]byte, 13)
	decoderConfig[0] = 0x40
	decoderConfig[1] = 0x15
	decoderConfig = append(decoderConfig, decoderSpecific...)
	esDescriptor := []byte{0x00, 0x01, 0x00}
	esDescriptor = append(esDescriptor, descriptor(0x04, decoderConfig)...)
	esDescriptor = append(esDescriptor, descriptor(0x06, []byte{0x02})...)
	return append(make([]byte, 4), descriptor(0x03, esDescriptor)...)
}

func descriptor(tag byte, payload []byte) []byte {
	if len(payload) >= 128 {
		panic("test descriptor payload exceeds single-byte length")
	}
	return append([]byte{tag, byte(len(payload))}, payload...)
}

func aacLCConfig(sampleRate uint32, channels uint16) []byte {
	frequencyIndex := -1
	for index, candidate := range aacSampleRates {
		if candidate == sampleRate {
			frequencyIndex = index
			break
		}
	}
	if frequencyIndex < 0 || channels < 1 || channels > 6 {
		return []byte{0, 0}
	}
	value := uint16(2<<11 | frequencyIndex<<7 | int(channels)<<3)
	return []byte{byte(value >> 8), byte(value)}
}

func nonAudioTrack(t testFataler) []byte {
	t.Helper()
	hdlr := make([]byte, 24)
	copy(hdlr[8:12], "vide")
	return box(t, "trak", box(t, "mdia", box(t, "hdlr", hdlr)))
}

func box(t testFataler, kind string, payload []byte) []byte {
	t.Helper()
	if len(kind) != 4 {
		t.Fatalf("box type %q must contain four bytes", kind)
	}
	result := make([]byte, 8, 8+len(payload))
	binary.BigEndian.PutUint32(result[0:4], uint32(8+len(payload)))
	copy(result[4:8], kind)
	return append(result, payload...)
}
