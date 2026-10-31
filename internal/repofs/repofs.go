// Package repofs reads files of an analysed repository. The repository is untrusted: a
// Makefile may `include ../../..`, a script may be a symlink to a secret, a "file" may be
// a fifo or /dev/zero. Every read here stays inside the repository root, accepts only
// regular files and is capped in size.
package repofs

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// MaxFileSize is the largest file that is read. Real Makefiles, scripts and workflows are
// kilobytes; anything beyond this is not a source file worth analysing.
const MaxFileSize = 4 << 20

var (
	// ErrOutside means the path leads out of the repository, by "..", an absolute path or a symlink.
	ErrOutside = errors.New("path escapes from the repository")
	// ErrNotRegular means the path is a directory, fifo, device or socket.
	ErrNotRegular = errors.New("not a regular file")
	// ErrTooLarge means the file is larger than MaxFileSize.
	ErrTooLarge = fmt.Errorf("file is larger than %d MiB", MaxFileSize>>20)
)

// ReadFile returns the content of root/rel, or an error wrapping ErrOutside, ErrNotRegular,
// ErrTooLarge or fs.ErrNotExist.
func ReadFile(root, rel string) ([]byte, error) {
	f, err := Open(root, rel)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// Read one byte more than allowed, so a file that grew after the size check is still caught.
	b, err := io.ReadAll(io.LimitReader(f, MaxFileSize+1))
	if err != nil {
		return nil, err
	}
	if len(b) > MaxFileSize {
		return nil, &fs.PathError{Op: "read", Path: rel, Err: ErrTooLarge}
	}
	return b, nil
}

// Open opens root/rel for reading under the same rules as ReadFile. The caller closes it.
func Open(root, rel string) (*os.File, error) {
	rt, err := stat(root, rel)
	if err != nil {
		return nil, err
	}
	defer rt.Close()
	f, err := rt.Open(rel)
	if err != nil {
		return nil, err
	}
	// The file may have been swapped between the check and the open.
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		f.Close()
		return nil, &fs.PathError{Op: "open", Path: rel, Err: ErrNotRegular}
	}
	return f, nil
}

// Exists reports whether root/rel is a readable regular file inside the repository.
func Exists(root, rel string) bool {
	rt, err := stat(root, rel)
	if err != nil {
		return false
	}
	rt.Close()
	return true
}

// stat checks the path before anything is opened: opening a fifo would block forever.
// On success the caller closes the returned Root.
func stat(root, rel string) (*os.Root, error) {
	if !filepath.IsLocal(rel) {
		return nil, &fs.PathError{Op: "open", Path: rel, Err: ErrOutside}
	}
	rt, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	fi, err := rt.Stat(rel) // follows symlinks, but only while they stay inside root
	switch {
	case err != nil:
	case !fi.Mode().IsRegular():
		err = &fs.PathError{Op: "open", Path: rel, Err: ErrNotRegular}
	case fi.Size() > MaxFileSize:
		err = &fs.PathError{Op: "open", Path: rel, Err: ErrTooLarge}
	}
	if err != nil {
		rt.Close()
		return nil, err
	}
	return rt, nil
}

// Reason is err without the "open path:" prefix, for warnings that already name the path.
func Reason(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return err.Error()
}
