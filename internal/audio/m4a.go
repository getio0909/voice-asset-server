package audio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	isoBoxHeaderSize = uint64(8)
	maxBrandBytes    = uint64(4 * 1024)
	maxESDSBytes     = uint64(64 * 1024)
	maxSampleEntries = uint32(128)
	minM4ASampleRate = uint32(1_000)
	maxM4ASampleRate = uint32(192_000)
	maxM4AChannels   = uint16(64)
)

// ErrInvalidM4A identifies malformed, truncated, ambiguous, or unsupported
// ISO BMFF audio input.
var ErrInvalidM4A = errors.New("invalid M4A")

// ProbeM4A validates an ISO BMFF file containing one AAC audio track and
// extracts bounded metadata without loading media payloads into memory.
func ProbeM4A(reader io.ReaderAt, size int64) (Metadata, error) {
	if reader == nil {
		return Metadata{}, invalidM4A("reader is nil")
	}
	if size < int64(isoBoxHeaderSize) {
		return Metadata{}, invalidM4A("file is shorter than an ISO box header")
	}

	var (
		foundFileType bool
		movie         *isoBox
		mediaBytes    uint64
	)
	err := walkISOBoxes(reader, 0, uint64(size), func(current isoBox) error {
		switch current.kind {
		case "ftyp":
			if foundFileType {
				return invalidM4A("duplicate ftyp box")
			}
			if err := validateFileType(reader, current); err != nil {
				return err
			}
			foundFileType = true
		case "moov":
			if movie != nil {
				return invalidM4A("duplicate moov box")
			}
			copy := current
			movie = &copy
		case "mdat":
			payloadSize := current.end - current.payloadStart
			if payloadSize == 0 || mediaBytes > ^uint64(0)-payloadSize {
				return invalidM4A("invalid media payload size")
			}
			mediaBytes += payloadSize
		}
		return nil
	})
	if err != nil {
		return Metadata{}, err
	}
	if !foundFileType {
		return Metadata{}, invalidM4A("missing ftyp box")
	}
	if movie == nil {
		return Metadata{}, invalidM4A("missing moov box")
	}
	if mediaBytes == 0 {
		return Metadata{}, invalidM4A("missing non-empty mdat box")
	}

	track, err := parseAudioTrack(reader, *movie)
	if err != nil {
		return Metadata{}, err
	}
	durationMS := milliseconds(track.duration, track.timescale)
	if durationMS <= 0 {
		return Metadata{}, invalidM4A("audio duration rounds to zero milliseconds")
	}
	if mediaBytes > uint64(^uint64(0)/8_000) {
		return Metadata{}, invalidM4A("media payload is too large")
	}

	return Metadata{
		Container:  "m4a",
		Codec:      "aac",
		SampleRate: track.sampleRate,
		Channels:   track.channels,
		Bitrate:    mediaBytes * 8_000 / uint64(durationMS),
		DurationMS: durationMS,
		DataBytes:  int64(mediaBytes),
	}, nil
}

// ProbeM4AFile extracts M4A metadata without changing the file position.
func ProbeM4AFile(file ProbeSource) (Metadata, error) {
	if file == nil {
		return Metadata{}, invalidM4A("file is nil")
	}
	info, err := file.Stat()
	if err != nil {
		return Metadata{}, fmt.Errorf("stat M4A file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return Metadata{}, invalidM4A("input is not a regular file")
	}
	return ProbeM4A(file, info.Size())
}

type isoBox struct {
	kind         string
	start        uint64
	payloadStart uint64
	end          uint64
}

type m4aTrack struct {
	timescale  uint32
	duration   uint64
	sampleRate uint32
	channels   uint16
}

func walkISOBoxes(reader io.ReaderAt, start, end uint64, visit func(isoBox) error) error {
	if start > end {
		return invalidM4A("invalid ISO box boundary")
	}
	for offset := start; offset < end; {
		current, err := readISOBox(reader, offset, end)
		if err != nil {
			return err
		}
		if err := visit(current); err != nil {
			return err
		}
		offset = current.end
	}
	return nil
}

func readISOBox(reader io.ReaderAt, offset, boundary uint64) (isoBox, error) {
	if boundary-offset < isoBoxHeaderSize {
		return isoBox{}, invalidM4A("truncated box header at offset %d", offset)
	}
	var header [16]byte
	if err := readAt(reader, header[:8], int64(offset)); err != nil {
		return isoBox{}, invalidM4A("read box header at offset %d: %v", offset, err)
	}
	size := uint64(binary.BigEndian.Uint32(header[0:4]))
	headerSize := isoBoxHeaderSize
	switch size {
	case 0:
		size = boundary - offset
	case 1:
		if boundary-offset < 16 {
			return isoBox{}, invalidM4A("truncated extended box header at offset %d", offset)
		}
		if err := readAt(reader, header[8:16], int64(offset+8)); err != nil {
			return isoBox{}, invalidM4A("read extended box header at offset %d: %v", offset, err)
		}
		size = binary.BigEndian.Uint64(header[8:16])
		headerSize = 16
	}
	if size < headerSize || size > boundary-offset {
		return isoBox{}, invalidM4A("box %q exceeds its boundary", string(header[4:8]))
	}
	return isoBox{
		kind:         string(header[4:8]),
		start:        offset,
		payloadStart: offset + headerSize,
		end:          offset + size,
	}, nil
}

func validateFileType(reader io.ReaderAt, box isoBox) error {
	payloadSize := box.end - box.payloadStart
	if payloadSize < 8 || payloadSize > maxBrandBytes || payloadSize%4 != 0 {
		return invalidM4A("invalid ftyp payload length")
	}
	payload := make([]byte, payloadSize)
	if err := readAt(reader, payload, int64(box.payloadStart)); err != nil {
		return invalidM4A("read ftyp payload: %v", err)
	}
	supported := supportedM4ABrand(string(payload[0:4]))
	for offset := 8; offset < len(payload); offset += 4 {
		supported = supported || supportedM4ABrand(string(payload[offset:offset+4]))
	}
	if !supported {
		return invalidM4A("ftyp does not declare a supported ISO BMFF brand")
	}
	return nil
}

func supportedM4ABrand(brand string) bool {
	switch brand {
	case "M4A ", "isom", "iso2", "mp41", "mp42":
		return true
	default:
		return false
	}
}

func parseAudioTrack(reader io.ReaderAt, movie isoBox) (m4aTrack, error) {
	var audioTracks []m4aTrack
	err := walkISOBoxes(reader, movie.payloadStart, movie.end, func(child isoBox) error {
		if child.kind != "trak" {
			return nil
		}
		track, isAudio, err := parseTrack(reader, child)
		if err != nil {
			return err
		}
		if isAudio {
			audioTracks = append(audioTracks, track)
		}
		return nil
	})
	if err != nil {
		return m4aTrack{}, err
	}
	if len(audioTracks) != 1 {
		return m4aTrack{}, invalidM4A("expected exactly one AAC audio track")
	}
	return audioTracks[0], nil
}

func parseTrack(reader io.ReaderAt, trackBox isoBox) (m4aTrack, bool, error) {
	var media *isoBox
	err := walkISOBoxes(reader, trackBox.payloadStart, trackBox.end, func(child isoBox) error {
		if child.kind == "mdia" {
			if media != nil {
				return invalidM4A("audio track contains duplicate mdia boxes")
			}
			copy := child
			media = &copy
		}
		return nil
	})
	if err != nil {
		return m4aTrack{}, false, err
	}
	if media == nil {
		return m4aTrack{}, false, nil
	}
	return parseMedia(reader, *media)
}

func parseMedia(reader io.ReaderAt, media isoBox) (m4aTrack, bool, error) {
	var mediaHeader, handler, mediaInfo *isoBox
	err := walkISOBoxes(reader, media.payloadStart, media.end, func(child isoBox) error {
		var target **isoBox
		switch child.kind {
		case "mdhd":
			target = &mediaHeader
		case "hdlr":
			target = &handler
		case "minf":
			target = &mediaInfo
		default:
			return nil
		}
		if *target != nil {
			return invalidM4A("duplicate %s box in media track", child.kind)
		}
		copy := child
		*target = &copy
		return nil
	})
	if err != nil {
		return m4aTrack{}, false, err
	}
	if handler == nil {
		return m4aTrack{}, false, nil
	}
	handlerType, err := readHandlerType(reader, *handler)
	if err != nil {
		return m4aTrack{}, false, err
	}
	if handlerType != "soun" {
		return m4aTrack{}, false, nil
	}
	if mediaHeader == nil || mediaInfo == nil {
		return m4aTrack{}, false, invalidM4A("audio track is missing mdhd or minf")
	}
	timescale, duration, err := readMediaDuration(reader, *mediaHeader)
	if err != nil {
		return m4aTrack{}, false, err
	}
	sampleRate, channels, err := readAudioSampleEntry(reader, *mediaInfo)
	if err != nil {
		return m4aTrack{}, false, err
	}
	return m4aTrack{
		timescale: timescale, duration: duration, sampleRate: sampleRate, channels: channels,
	}, true, nil
}

func readHandlerType(reader io.ReaderAt, box isoBox) (string, error) {
	if box.end-box.payloadStart < 12 {
		return "", invalidM4A("hdlr box is too short")
	}
	var header [12]byte
	if err := readAt(reader, header[:], int64(box.payloadStart)); err != nil {
		return "", invalidM4A("read hdlr box: %v", err)
	}
	return string(header[8:12]), nil
}

func readMediaDuration(reader io.ReaderAt, box isoBox) (uint32, uint64, error) {
	if box.end-box.payloadStart < 20 {
		return 0, 0, invalidM4A("mdhd box is too short")
	}
	var version [1]byte
	if err := readAt(reader, version[:], int64(box.payloadStart)); err != nil {
		return 0, 0, invalidM4A("read mdhd version: %v", err)
	}
	var timescale uint32
	var duration uint64
	switch version[0] {
	case 0:
		var fields [20]byte
		if err := readAt(reader, fields[:], int64(box.payloadStart)); err != nil {
			return 0, 0, invalidM4A("read version 0 mdhd box: %v", err)
		}
		timescale = binary.BigEndian.Uint32(fields[12:16])
		duration = uint64(binary.BigEndian.Uint32(fields[16:20]))
		if duration == uint64(^uint32(0)) {
			return 0, 0, invalidM4A("mdhd duration is unspecified")
		}
	case 1:
		if box.end-box.payloadStart < 32 {
			return 0, 0, invalidM4A("version 1 mdhd box is too short")
		}
		var fields [32]byte
		if err := readAt(reader, fields[:], int64(box.payloadStart)); err != nil {
			return 0, 0, invalidM4A("read version 1 mdhd box: %v", err)
		}
		timescale = binary.BigEndian.Uint32(fields[20:24])
		duration = binary.BigEndian.Uint64(fields[24:32])
		if duration == ^uint64(0) {
			return 0, 0, invalidM4A("mdhd duration is unspecified")
		}
	default:
		return 0, 0, invalidM4A("unsupported mdhd version %d", version[0])
	}
	if timescale == 0 || duration == 0 {
		return 0, 0, invalidM4A("mdhd timescale and duration must be positive")
	}
	return timescale, duration, nil
}

func readAudioSampleEntry(reader io.ReaderAt, mediaInfo isoBox) (uint32, uint16, error) {
	var sampleTable *isoBox
	if err := walkISOBoxes(reader, mediaInfo.payloadStart, mediaInfo.end, func(child isoBox) error {
		if child.kind == "stbl" {
			if sampleTable != nil {
				return invalidM4A("duplicate stbl box")
			}
			copy := child
			sampleTable = &copy
		}
		return nil
	}); err != nil {
		return 0, 0, err
	}
	if sampleTable == nil {
		return 0, 0, invalidM4A("audio track is missing stbl")
	}

	var sampleDescription *isoBox
	if err := walkISOBoxes(reader, sampleTable.payloadStart, sampleTable.end, func(child isoBox) error {
		if child.kind == "stsd" {
			if sampleDescription != nil {
				return invalidM4A("duplicate stsd box")
			}
			copy := child
			sampleDescription = &copy
		}
		return nil
	}); err != nil {
		return 0, 0, err
	}
	if sampleDescription == nil || sampleDescription.end-sampleDescription.payloadStart < 8 {
		return 0, 0, invalidM4A("audio track is missing a valid stsd box")
	}

	var stsdHeader [8]byte
	if err := readAt(reader, stsdHeader[:], int64(sampleDescription.payloadStart)); err != nil {
		return 0, 0, invalidM4A("read stsd header: %v", err)
	}
	entryCount := binary.BigEndian.Uint32(stsdHeader[4:8])
	if entryCount == 0 || entryCount > maxSampleEntries {
		return 0, 0, invalidM4A("stsd entry count is invalid")
	}

	var (
		seenEntries uint32
		audioEntry  *isoBox
	)
	err := walkISOBoxes(reader, sampleDescription.payloadStart+8, sampleDescription.end, func(child isoBox) error {
		seenEntries++
		if child.kind == "mp4a" {
			if audioEntry != nil {
				return invalidM4A("duplicate mp4a sample entry")
			}
			copy := child
			audioEntry = &copy
		}
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	if seenEntries != entryCount || audioEntry == nil {
		return 0, 0, invalidM4A("stsd does not contain one declared mp4a entry")
	}
	if audioEntry.end-audioEntry.start < 36 {
		return 0, 0, invalidM4A("mp4a sample entry is too short")
	}
	var version [2]byte
	if err := readAt(reader, version[:], int64(audioEntry.start+16)); err != nil {
		return 0, 0, invalidM4A("read mp4a sample entry version: %v", err)
	}
	if binary.BigEndian.Uint16(version[:]) != 0 {
		return 0, 0, invalidM4A("unsupported mp4a sample entry version")
	}
	var fields [12]byte
	if err := readAt(reader, fields[:], int64(audioEntry.start+24)); err != nil {
		return 0, 0, invalidM4A("read mp4a sample entry: %v", err)
	}
	channels := binary.BigEndian.Uint16(fields[0:2])
	fixedSampleRate := binary.BigEndian.Uint32(fields[8:12])
	sampleRate := fixedSampleRate >> 16
	if fixedSampleRate&0xffff != 0 || sampleRate < minM4ASampleRate || sampleRate > maxM4ASampleRate {
		return 0, 0, invalidM4A("mp4a sample rate is invalid")
	}
	if channels == 0 || channels > maxM4AChannels {
		return 0, 0, invalidM4A("mp4a channel count is invalid")
	}
	if err := validateAACDescriptor(reader, *audioEntry, sampleRate, channels); err != nil {
		return 0, 0, err
	}
	return sampleRate, channels, nil
}

func validateAACDescriptor(
	reader io.ReaderAt,
	audioEntry isoBox,
	expectedSampleRate uint32,
	expectedChannels uint16,
) error {
	var elementaryStreamDescriptor *isoBox
	if err := walkISOBoxes(reader, audioEntry.start+36, audioEntry.end, func(child isoBox) error {
		if child.kind != "esds" {
			return nil
		}
		if elementaryStreamDescriptor != nil {
			return invalidM4A("duplicate esds box in mp4a sample entry")
		}
		copy := child
		elementaryStreamDescriptor = &copy
		return nil
	}); err != nil {
		return err
	}
	if elementaryStreamDescriptor == nil {
		return invalidM4A("mp4a sample entry is missing esds")
	}
	payloadSize := elementaryStreamDescriptor.end - elementaryStreamDescriptor.payloadStart
	if payloadSize < 6 || payloadSize > maxESDSBytes {
		return invalidM4A("esds payload length is invalid")
	}
	payload := make([]byte, int(payloadSize))
	if err := readAt(reader, payload, int64(elementaryStreamDescriptor.payloadStart)); err != nil {
		return invalidM4A("read esds payload: %v", err)
	}
	if payload[0] != 0 || payload[1] != 0 || payload[2] != 0 || payload[3] != 0 {
		return invalidM4A("unsupported esds version or flags")
	}
	tag, esPayload, next, err := readDescriptor(payload[4:], 0)
	if err != nil || tag != 0x03 || next != len(payload)-4 {
		return invalidM4A("esds does not contain one valid ES descriptor")
	}
	return validateESDescriptor(esPayload, expectedSampleRate, expectedChannels)
}

func validateESDescriptor(payload []byte, expectedSampleRate uint32, expectedChannels uint16) error {
	if len(payload) < 3 {
		return invalidM4A("ES descriptor is too short")
	}
	flags := payload[2]
	offset := 3
	if flags&0x80 != 0 {
		offset += 2
	}
	if flags&0x40 != 0 {
		if offset >= len(payload) {
			return invalidM4A("ES descriptor URL length is missing")
		}
		offset++
		offset += int(payload[offset-1])
	}
	if flags&0x20 != 0 {
		offset += 2
	}
	if offset >= len(payload) {
		return invalidM4A("ES descriptor is missing decoder configuration")
	}
	tag, decoderConfig, _, err := readDescriptor(payload, offset)
	if err != nil || tag != 0x04 {
		return invalidM4A("ES descriptor is missing a valid decoder configuration")
	}
	return validateAACDecoderConfig(decoderConfig, expectedSampleRate, expectedChannels)
}

func validateAACDecoderConfig(payload []byte, expectedSampleRate uint32, expectedChannels uint16) error {
	if len(payload) < 13 {
		return invalidM4A("decoder configuration is too short")
	}
	if payload[0] != 0x40 || payload[1]>>2 != 0x05 || payload[1]&0x01 == 0 {
		return invalidM4A("mp4a decoder is not MPEG-4 audio")
	}
	var audioSpecificConfig []byte
	for offset := 13; offset < len(payload); {
		tag, descriptorPayload, next, err := readDescriptor(payload, offset)
		if err != nil {
			return invalidM4A("decoder configuration contains an invalid descriptor")
		}
		if tag == 0x05 {
			if audioSpecificConfig != nil {
				return invalidM4A("decoder configuration contains duplicate AudioSpecificConfig")
			}
			audioSpecificConfig = descriptorPayload
		}
		offset = next
	}
	if audioSpecificConfig == nil {
		return invalidM4A("decoder configuration is missing AudioSpecificConfig")
	}
	audioObjectType, sampleRate, channels, err := parseAudioSpecificConfig(audioSpecificConfig)
	if err != nil || !supportedAACObjectType(audioObjectType) {
		return invalidM4A("AudioSpecificConfig does not declare supported AAC")
	}
	if sampleRate != expectedSampleRate || channels != expectedChannels {
		return invalidM4A("AudioSpecificConfig conflicts with the mp4a sample entry")
	}
	return nil
}

func readDescriptor(data []byte, offset int) (byte, []byte, int, error) {
	if offset < 0 || offset >= len(data) {
		return 0, nil, 0, errors.New("descriptor tag is missing")
	}
	tag := data[offset]
	offset++
	length := 0
	terminated := false
	for range 4 {
		if offset >= len(data) {
			return 0, nil, 0, errors.New("descriptor length is truncated")
		}
		current := data[offset]
		offset++
		length = length<<7 | int(current&0x7f)
		if current&0x80 == 0 {
			terminated = true
			break
		}
	}
	if !terminated || length > len(data)-offset {
		return 0, nil, 0, errors.New("descriptor exceeds its boundary")
	}
	return tag, data[offset : offset+length], offset + length, nil
}

func parseAudioSpecificConfig(data []byte) (int, uint32, uint16, error) {
	bits := bitReader{data: data}
	audioObjectType, err := readAudioObjectType(&bits)
	if err != nil {
		return 0, 0, 0, err
	}
	frequencyIndex, err := bits.read(4)
	if err != nil {
		return 0, 0, 0, err
	}
	var sampleRate uint32
	if frequencyIndex == 15 {
		explicitFrequency, readErr := bits.read(24)
		if readErr != nil || explicitFrequency < minM4ASampleRate || explicitFrequency > maxM4ASampleRate {
			return 0, 0, 0, errors.New("explicit AAC sample rate is invalid")
		}
		sampleRate = explicitFrequency
	} else {
		if int(frequencyIndex) >= len(aacSampleRates) {
			return 0, 0, 0, errors.New("AAC sample-rate index is invalid")
		}
		sampleRate = aacSampleRates[frequencyIndex]
	}
	channelConfiguration, err := bits.read(4)
	if err != nil || channelConfiguration == 0 || channelConfiguration > 7 {
		return 0, 0, 0, errors.New("AAC channel configuration is unsupported")
	}
	channels := uint16(channelConfiguration)
	if channelConfiguration == 7 {
		channels = 8
	}
	return audioObjectType, sampleRate, channels, nil
}

func readAudioObjectType(bits *bitReader) (int, error) {
	value, err := bits.read(5)
	if err != nil {
		return 0, err
	}
	if value != 31 {
		return int(value), nil
	}
	extension, err := bits.read(6)
	if err != nil {
		return 0, err
	}
	return 32 + int(extension), nil
}

func supportedAACObjectType(audioObjectType int) bool {
	switch audioObjectType {
	case 1, 2, 3, 4, 6, 17, 19, 20, 23, 39:
		return true
	default:
		return false
	}
}

type bitReader struct {
	data      []byte
	bitOffset int
}

func (r *bitReader) read(count int) (uint32, error) {
	if count < 1 || count > 24 || r.bitOffset+count > len(r.data)*8 {
		return 0, errors.New("AudioSpecificConfig is truncated")
	}
	var value uint32
	for range count {
		byteOffset := r.bitOffset / 8
		bitInByte := 7 - r.bitOffset%8
		value = value<<1 | uint32((r.data[byteOffset]>>bitInByte)&1)
		r.bitOffset++
	}
	return value, nil
}

var aacSampleRates = [...]uint32{
	96_000, 88_200, 64_000, 48_000, 44_100, 32_000, 24_000,
	22_050, 16_000, 12_000, 11_025, 8_000, 7_350,
}

func milliseconds(duration uint64, timescale uint32) int64 {
	whole := duration / uint64(timescale)
	remainder := duration % uint64(timescale)
	if whole > uint64(^uint64(0)/1_000) {
		return 0
	}
	value := whole*1_000 + remainder*1_000/uint64(timescale)
	if value > uint64(^uint64(0)>>1) {
		return 0
	}
	return int64(value)
}

func invalidM4A(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidM4A, fmt.Sprintf(format, args...))
}
