package runtimecell

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// ReadDirShared lists the directory dir in name order, like os.ReadDir. On Windows it opens the directory with every share mode,
// FILE_SHARE_DELETE included, which os.ReadDir's open does not: a handle without FILE_SHARE_DELETE makes any other process's
// delete or rename of that directory fail with a sharing violation while it is open, and a directory that another process holds
// open for delete (every os.RemoveAll of it does, for an instant) makes that open fail with one. A source hash walks trees that a
// build running beside it fills and empties, so it must neither block the build nor fail on it.
func ReadDirShared(dir string) ([]fs.DirEntry, error) { return readDirShared(dir) }

// sharedDirFS is os.DirFS(root) whose directory listings go through ReadDirShared.
type sharedDirFS struct {
	fs.FS
	root string
}

func newSharedDirFS(root string) fs.FS { return sharedDirFS{FS: os.DirFS(root), root: root} }

func (s sharedDirFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrInvalid}
	}
	entries, err := ReadDirShared(filepath.Join(s.root, filepath.FromSlash(name)))
	if pathErr, ok := errors.AsType[*fs.PathError](err); ok {
		pathErr.Path = name
	}
	return entries, err
}

// Stat answers from the directory entry without opening the name (os.Stat), not through Open as fs.Stat does for an FS without a
// Stat method: the open of a directory that denies listing by ACL fails where its attributes can be read, and the vanish check of a
// directory that failed to list needs exactly that difference to tell a live directory from one being deleted.
func (s sharedDirFS) Stat(name string) (fs.FileInfo, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrInvalid}
	}
	info, err := os.Stat(filepath.Join(s.root, filepath.FromSlash(name))) //perf:ok one stat per directory that failed to list mid-walk
	if pathErr, ok := errors.AsType[*fs.PathError](err); ok {
		pathErr.Path = name
	}
	return info, err
}

// WalkDirShared is filepath.WalkDir with ReadDirShared as its directory listing, with the same order and the same handling of
// the errors fn returns.
func WalkDirShared(root string, fn fs.WalkDirFunc) error {
	info, err := os.Lstat(root) //perf:ok once per walk root
	if err != nil {
		err = fn(root, nil, err)
	} else {
		err = walkDirShared(root, fs.FileInfoToDirEntry(info), fn)
	}
	if errors.Is(err, filepath.SkipDir) || errors.Is(err, filepath.SkipAll) {
		return nil
	}
	return err
}

func walkDirShared(path string, d fs.DirEntry, fn fs.WalkDirFunc) error {
	if err := fn(path, d, nil); err != nil || !d.IsDir() {
		if errors.Is(err, filepath.SkipDir) && d.IsDir() {
			err = nil
		}
		return err
	}
	dirs, err := ReadDirShared(path)
	if err != nil {
		// The directory could not be listed: fn decides whether the walk goes on, as filepath.WalkDir does.
		err = fn(path, d, err)
		if err != nil {
			if errors.Is(err, filepath.SkipDir) && d.IsDir() {
				err = nil
			}
			return err
		}
	}
	for _, entry := range dirs {
		if err := walkDirShared(filepath.Join(path, entry.Name()), entry, fn); err != nil {
			if errors.Is(err, filepath.SkipDir) {
				break
			}
			return err
		}
	}
	return nil
}
