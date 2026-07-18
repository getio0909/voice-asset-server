package audio

import (
	"fmt"
	"io"
	"os"
)

// ProbeSource is the seekable subset required by media parsers. Both local
// files and materialized remote objects satisfy it.
type ProbeSource interface {
	io.Reader
	io.ReaderAt
	io.Seeker
	Stat() (os.FileInfo, error)
}

// ProbeFile validates and extracts metadata according to the declared media
// type. It never falls back to extension- or signature-only guessing.
func ProbeFile(file ProbeSource, mimeType string) (Metadata, error) {
	switch mimeType {
	case "audio/wav", "audio/x-wav":
		return ProbeWAVFile(file)
	case "audio/mp4":
		return ProbeM4AFile(file)
	default:
		return Metadata{}, fmt.Errorf("unsupported audio media type %q", mimeType)
	}
}
