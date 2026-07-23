package fs

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"hybrid-ftp/internal/session"
)

func ResolvePath(sess *session.Session, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(sess.CWD, path))
}

func ResolveSafe(baseRoot string, sess *session.Session, relPath string) (string, error) {
	target := ResolvePath(sess, relPath)
	absRoot, err := filepath.Abs(baseRoot)
	if err != nil {
		return "", fmt.Errorf("resolve root: %w", err)
	}
	fullPath := filepath.Clean(filepath.Join(absRoot, target))
	rel, err := filepath.Rel(absRoot, fullPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("path outside root: %s", relPath)
	}
	if fullPath != absRoot && !strings.HasPrefix(fullPath, absRoot+string(os.PathSeparator)) {
		return "", fmt.Errorf("path outside root: %s", relPath)
	}
	return fullPath, nil
}

func ListDir(dirPath string) ([]os.FileInfo, error) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", dirPath, err)
	}
	infos := make([]os.FileInfo, len(entries))
	for i, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("info %s: %w", entry.Name(), err)
		}
		infos[i] = info
	}
	log.Printf("[FS] listed %s: %d entries", dirPath, len(infos))
	return infos, nil
}

func OpenForRead(filePath string) (*os.File, os.FileInfo, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, nil, fmt.Errorf("open for read %s: %w", filePath, err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, fmt.Errorf("stat %s: %w", filePath, err)
	}
	if info.IsDir() {
		f.Close()
		return nil, nil, fmt.Errorf("cannot RETR a directory: %s", filePath)
	}
	log.Printf("[FS] opened %s for read (%d bytes)", filePath, info.Size())
	return f, info, nil
}

func OpenForWrite(filePath string) (*os.File, error) {
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	f, err := os.Create(filePath)
	if err != nil {
		return nil, fmt.Errorf("open for write %s: %w", filePath, err)
	}
	log.Printf("[FS] opened %s for write", filePath)
	return f, nil
}

func FormatListEntry(info os.FileInfo) string {
	entryType := "-"
	if info.IsDir() {
		entryType = "d"
	}
	mode := info.Mode().String()
	size := info.Size()
	name := info.Name()
	modTime := info.ModTime().Format("Jan 02 15:04")
	return fmt.Sprintf("%s%s %8d %s %s\r\n", entryType, mode[1:], size, modTime, name)
}

func FormatList(infos []os.FileInfo) string {
	var sb strings.Builder
	for _, info := range infos {
		sb.WriteString(FormatListEntry(info))
	}
	return sb.String()
}
