package storage

import (
	"context"
	"io"
	"os"
)

// File is a seekable object snapshot. Remote drivers may materialize a
// verified temporary file and remove it when Close is called.
type File interface {
	io.Reader
	io.ReaderAt
	io.Seeker
	io.Closer
	Stat() (os.FileInfo, error)
	Name() string
}

// Driver is the complete immutable-object boundary shared by API and Worker.
// Implementations must preserve create-only publication and full-integrity
// deletion semantics.
type Driver interface {
	Backend() Backend
	PutPart(context.Context, string, int, io.Reader, PutPartOptions) (Part, error)
	Assemble(context.Context, string, string, []PartRef, AssembleOptions) (Object, error)
	PutImmutable(context.Context, string, string, string, io.Reader, int64) (Object, error)
	// PutSnapshot restores an already-issued immutable key after verifying its
	// exact size and SHA-256. It is reserved for clean backup restores.
	PutSnapshot(context.Context, string, io.Reader, int64, string) (Object, error)
	Open(context.Context, string) (File, error)
	DeleteParts(context.Context, string) error
	DeleteObject(context.Context, string, int64, string) error
}

var _ Driver = (*Local)(nil)
