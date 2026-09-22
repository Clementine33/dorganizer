package inventory

import "context"

// DirEntry is one directory or file a traversal emitted: the observed fact the
// scan pipeline stages, before a session and a format are attached to it.
type DirEntry struct {
	Path       string
	ParentPath string
	Name       string
	IsDir      bool
	Size       int64
	Mtime      int64
}

// WalkRoot is one full-root traversal strategy: parallel directory descent
// under the root, emitting every directory and file it finds. The pipeline
// reads the filesystem through this port, so the traversal lives in the
// filesystem adapter and the pipeline stays testable without one.
type WalkRoot func(
	ctx context.Context,
	rootPath, basePath string,
	dirConcurrency int,
	emit func(DirEntry) error,
) error

// WalkMember is the single-enumerator traversal of one member directory: the
// refresh path reads one subtree with one enumerator, which is what makes a
// page refresh cheap next to a full scan.
type WalkMember func(ctx context.Context, folderPath, basePath string, emit func(DirEntry) error) error
