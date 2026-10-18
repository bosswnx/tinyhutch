package main

import (
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	uploadTempPrefix = ".hutch-upload-"
	trashDir         = ".hutch-trash"
	maxListEntries   = 5000
)

var (
	errBadPath  = errors.New("bad path")
	errNotFound = errors.New("not found")
	errExists   = errors.New("already exists")
	errTooLarge = errors.New("too large")
	errIsDir    = errors.New("is a directory")
)

type entryInfo struct {
	Name    string
	IsDir   bool
	Size    int64
	ModTime time.Time
}

type fileSystem struct {
	root      *os.Root
	cwd       string
	protected map[string]struct{}
}

func newFileSystem(cwd, configPath, secretPath string) (*fileSystem, error) {
	root, err := os.OpenRoot(cwd)
	if err != nil {
		return nil, err
	}
	protected := make(map[string]struct{})
	add := func(abs string) {
		if abs == "" {
			return
		}
		rel, err := filepath.Rel(cwd, abs)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return
		}
		protected[filepath.ToSlash(rel)] = struct{}{}
	}
	protected[trashDir] = struct{}{}
	add(configPath)
	add(configPath + ".bak")
	add(configPath + ".tmp")
	add(secretPath)
	// Default names are always protected, even if --config points elsewhere.
	for _, name := range []string{
		"config.json", "config.json.bak", "config.json.tmp",
		".hutch.secret",
	} {
		add(filepath.Join(cwd, name))
	}
	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			add(resolved)
		}
		add(exe)
	}
	return &fileSystem{root: root, cwd: cwd, protected: protected}, nil
}

func (f *fileSystem) Close() error { return f.root.Close() }

func (f *fileSystem) pathFor(rel string) string {
	if rel == "" {
		return "."
	}
	return rel
}

func (f *fileSystem) isProtected(rel string) bool {
	if _, ok := f.protected[rel]; ok {
		return true
	}
	return strings.HasPrefix(rel, trashDir+"/")
}

func isTempName(name string) bool { return strings.HasPrefix(name, uploadTempPrefix) }

func hasHiddenComponent(rel string) bool {
	for _, part := range strings.Split(rel, "/") {
		if strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}

// guard validates a relative path before any filesystem operation.
// finalMayMiss allows the last component to not exist yet (for creates).
func (f *fileSystem) guard(rel string, showHidden, finalMayMiss bool) error {
	if rel == "" {
		return nil
	}
	parts := strings.Split(rel, "/")
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			return errBadPath
		}
		if isTempName(p) {
			return errNotFound
		}
		if !showHidden && strings.HasPrefix(p, ".") {
			return errNotFound
		}
	}
	if f.isProtected(rel) {
		return errNotFound
	}
	cur := ""
	for i, p := range parts {
		cur = joinRel(cur, p)
		info, err := f.root.Lstat(cur)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && finalMayMiss && i == len(parts)-1 {
				return nil
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errNotFound
		}
	}
	return nil
}

// visible decides whether a directory entry may be listed.
func (f *fileSystem) visible(rel, name string, showHidden bool) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	if isTempName(name) {
		return false
	}
	if !showHidden && strings.HasPrefix(name, ".") {
		return false
	}
	return !f.isProtected(joinRel(rel, name))
}

func (f *fileSystem) list(rel string, showHidden bool) ([]entryInfo, bool, error) {
	dir, err := f.root.Open(f.pathFor(rel))
	if err != nil {
		return nil, false, err
	}
	des, err := dir.ReadDir(-1)
	dir.Close()
	if err != nil {
		return nil, false, err
	}
	out := make([]entryInfo, 0, len(des))
	for _, de := range des {
		name := de.Name()
		if !f.visible(rel, name, showHidden) {
			continue
		}
		if de.Type()&os.ModeSymlink != 0 {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		isDir := info.IsDir()
		if !isDir && !info.Mode().IsRegular() {
			continue
		}
		out = append(out, entryInfo{
			Name:    name,
			IsDir:   isDir,
			Size:    sizeForEntry(isDir, info.Size()),
			ModTime: info.ModTime(),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return out[i].Name < out[j].Name
	})
	truncated := len(out) > maxListEntries
	if truncated {
		out = out[:maxListEntries]
	}
	return out, truncated, nil
}

func (f *fileSystem) stat(rel string) (os.FileInfo, error) {
	return f.root.Stat(f.pathFor(rel))
}

func sizeForEntry(isDir bool, size int64) int64 {
	if isDir {
		return 0
	}
	return size
}

func (f *fileSystem) open(rel string) (*os.File, os.FileInfo, error) {
	file, err := f.root.Open(f.pathFor(rel))
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	return file, info, nil
}

func (f *fileSystem) mkdir(rel string) error {
	return f.root.Mkdir(rel, 0o755)
}

func (f *fileSystem) rename(from, to string) error {
	return f.root.Rename(from, to)
}

func (f *fileSystem) remove(rel string, recursive bool) error {
	info, err := f.root.Lstat(rel)
	if err != nil {
		return err
	}
	if info.IsDir() {
		if recursive {
			return f.root.RemoveAll(rel)
		}
		return f.root.Remove(rel)
	}
	return f.root.Remove(rel)
}

// saveUpload streams r into dir/name through a temp file, then renames it into
// place. It returns the number of bytes written.
func (f *fileSystem) saveUpload(dir, name string, overwrite bool, maxBytes int64, r io.Reader) (int64, error) {
	target := joinRel(dir, name)
	if info, err := f.root.Lstat(target); err == nil {
		if info.IsDir() {
			return 0, errIsDir
		}
		if !overwrite {
			return 0, errExists
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return 0, err
	}

	suffix, err := randomHex(8)
	if err != nil {
		return 0, err
	}
	tmp := joinRel(dir, uploadTempPrefix+suffix+".part")
	file, err := f.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return 0, err
	}
	n, copyErr := io.Copy(file, io.LimitReader(r, maxBytes+1))
	if copyErr == nil && n > maxBytes {
		copyErr = errTooLarge
	}
	if copyErr == nil {
		copyErr = file.Sync()
	}
	if closeErr := file.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		f.root.Remove(tmp)
		return n, copyErr
	}
	if err := f.root.Rename(tmp, target); err != nil {
		f.root.Remove(tmp)
		return n, err
	}
	return n, nil
}

// cleanupTrash drops any undo trash left over from a previous run.
func (f *fileSystem) cleanupTrash() {
	if err := f.root.RemoveAll(trashDir); err != nil {
		slog.Warn("清理回收站失败", "err", err)
	}
}

// cleanupOldUploads removes stale upload temp files. It runs once at startup.
func (f *fileSystem) cleanupOldUploads(maxAge time.Duration) (int, error) {
	cutoff := time.Now().Add(-maxAge)
	removed := 0
	err := fs.WalkDir(f.root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !isTempName(d.Name()) {
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			return nil
		}
		if err := f.root.Remove(p); err == nil {
			removed++
		}
		return nil
	})
	return removed, err
}
