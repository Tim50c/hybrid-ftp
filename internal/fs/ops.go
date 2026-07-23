package fs

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

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

func ChangeDir(root string, sess *session.Session, path string) error {
	fullPath, err := ResolveSafe(root, sess, path)
	if err != nil {
		return fmt.Errorf("change dir: %w", err)
	}
	info, err := os.Stat(fullPath)
	if err != nil {
		return fmt.Errorf("change dir stat: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory: %s", path)
	}
	sess.CWD = ResolvePath(sess, path)
	log.Printf("[FS] cwd changed to %s", sess.CWD)
	return nil
}

func MakeDir(root string, sess *session.Session, dirname string) error {
	fullPath, err := ResolveSafe(root, sess, dirname)
	if err != nil {
		return fmt.Errorf("make dir: %w", err)
	}
	if err := os.Mkdir(fullPath, 0755); err != nil {
		return fmt.Errorf("make dir: %w", err)
	}
	log.Printf("[FS] created directory %s", fullPath)
	return nil
}

func RemoveDir(root string, sess *session.Session, dirname string) error {
	fullPath, err := ResolveSafe(root, sess, dirname)
	if err != nil {
		return fmt.Errorf("remove dir: %w", err)
	}
	if err := os.Remove(fullPath); err != nil {
		return fmt.Errorf("remove dir: %w", err)
	}
	log.Printf("[FS] removed directory %s", fullPath)
	return nil
}

func FormatList(infos []os.FileInfo) string {
	var sb strings.Builder
	for _, info := range infos {
		sb.WriteString(FormatListEntry(info))
	}
	return sb.String()
}

func DeleteFile(root string, sess *session.Session, filename string) error {
	fullPath, err := ResolveSafe(root, sess, filename)
	if err != nil {
		return fmt.Errorf("delete file: %w", err)
	}
	if err := os.Remove(fullPath); err != nil {
		return fmt.Errorf("delete file: %w", err)
	}
	log.Printf("[FS] deleted %s", fullPath)
	return nil
}

func NameList(dirPath string) ([]string, error) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, fmt.Errorf("name list %s: %w", dirPath, err)
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	log.Printf("[FS] name list %s: %d entries", dirPath, len(names))
	return names, nil
}

func FileSize(filePath string) (int64, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		return 0, fmt.Errorf("file size: %w", err)
	}
	if info.IsDir() {
		return 0, fmt.Errorf("cannot SIZE a directory: %s", filePath)
	}
	return info.Size(), nil
}

func ModTime(filePath string) (time.Time, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		return time.Time{}, fmt.Errorf("mod time: %w", err)
	}
	return info.ModTime(), nil
}

func StatPath(root string, sess *session.Session, path string) (string, error) {
	fullPath, err := ResolveSafe(root, sess, path)
	if err != nil {
		return "", fmt.Errorf("stat: %w", err)
	}
	info, err := os.Stat(fullPath)
	if err != nil {
		return "", fmt.Errorf("stat: %w", err)
	}
	var result string
	if info.IsDir() {
		result = fmt.Sprintf("Directory: %s\r\nPermissions: %s\r\nModified: %s\r\n",
			info.Name(), info.Mode(), info.ModTime().Format("20060102150405"))
	} else {
		result = fmt.Sprintf("File: %s\r\nSize: %d bytes\r\nPermissions: %s\r\nModified: %s\r\n",
			info.Name(), info.Size(), info.Mode(), info.ModTime().Format("20060102150405"))
	}
	return result, nil
}

func OpenForAppend(filePath string) (*os.File, error) {
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("mkdir for append: %w", err)
	}
	f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("open for append: %w", err)
	}
	log.Printf("[FS] opened %s for append", filePath)
	return f, nil
}
