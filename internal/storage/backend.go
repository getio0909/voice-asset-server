package storage

import (
	"errors"
	"fmt"
	"strings"
)

var ErrInvalidBackend = errors.New("invalid storage backend")

// Backend identifies the durable system that owns an object key. Values are
// persisted in asset_objects and therefore form part of the storage contract.
type Backend string

const (
	BackendLocal Backend = "local"
	BackendS3    Backend = "s3"
)

// ParseBackend normalizes a configured backend and rejects unknown values.
func ParseBackend(value string) (Backend, error) {
	backend := Backend(strings.ToLower(strings.TrimSpace(value)))
	if !backend.Valid() {
		return "", fmt.Errorf("%w: %q", ErrInvalidBackend, value)
	}
	return backend, nil
}

// Valid reports whether the backend can be persisted by the current schema.
func (backend Backend) Valid() bool {
	return backend == BackendLocal || backend == BackendS3
}
