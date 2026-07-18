package storage_test

import (
	"errors"
	"testing"

	"github.com/getio0909/voice-asset-server/internal/storage"
)

func TestParseBackend(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  storage.Backend
	}{
		{name: "local", value: " local ", want: storage.BackendLocal},
		{name: "s3", value: "S3", want: storage.BackendS3},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := storage.ParseBackend(test.value)
			if err != nil || got != test.want {
				t.Fatalf("ParseBackend(%q) = %q, %v; want %q", test.value, got, err, test.want)
			}
		})
	}
}

func TestParseBackendRejectsUnknownValue(t *testing.T) {
	if _, err := storage.ParseBackend("filesystem"); !errors.Is(err, storage.ErrInvalidBackend) {
		t.Fatalf("ParseBackend() error = %v, want ErrInvalidBackend", err)
	}
}
