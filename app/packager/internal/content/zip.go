package content

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"speedapp-packager/app/packager/internal/config"
)

type FileInfo struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Extract 在独立临时目录中验证全部文件和 CRC，成功后才发布 html 目录。
// ZIP 必须直接包含 index.html；不扁平化或丢弃 Cocos 的目录结构。
func Extract(archive, destination string, limits config.DownloadConfig) (map[string]FileInfo, error) {
	z, err := zip.OpenReader(archive)
	if err != nil {
		return nil, fmt.Errorf("invalid ZIP: %w", err)
	}
	defer z.Close()
	if len(z.File) == 0 || len(z.File) > limits.MaxEntries {
		return nil, fmt.Errorf("ZIP entry count exceeds limit or is empty")
	}
	if _, err = os.Lstat(destination); !os.IsNotExist(err) {
		return nil, fmt.Errorf("destination must not exist")
	}
	stage, err := os.MkdirTemp(filepath.Dir(destination), ".html-stage-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	files := make(map[string]FileInfo)
	seen := make(map[string]bool)
	var total int64
	for _, entry := range z.File {
		name := strings.TrimSuffix(entry.Name, "/")
		if name == "" || len(name) > 1024 || strings.ContainsAny(name, "\\:\x00") || strings.HasPrefix(name, "/") {
			return nil, fmt.Errorf("unsafe ZIP path")
		}
		for _, part := range strings.Split(name, "/") {
			if part == "" || part == "." || part == ".." || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
				return nil, fmt.Errorf("unsafe ZIP path segment")
			}
		}
		key := strings.ToLower(name)
		if seen[key] {
			return nil, fmt.Errorf("duplicate ZIP path")
		}
		seen[key] = true
		path := filepath.Join(stage, filepath.FromSlash(name))
		if entry.Mode().IsDir() {
			if err = os.MkdirAll(path, 0700); err != nil {
				return nil, err
			}
			continue
		}
		if !entry.Mode().IsRegular() {
			return nil, fmt.Errorf("ZIP links and special files are forbidden")
		}
		if entry.UncompressedSize64 > uint64(limits.MaxFileBytes) || entry.UncompressedSize64 > uint64(limits.MaxExpandedBytes-total) {
			return nil, fmt.Errorf("ZIP expanded size exceeds limit")
		}
		if entry.UncompressedSize64 > 0 && (entry.CompressedSize64 == 0 || entry.UncompressedSize64/entry.CompressedSize64 > uint64(limits.MaxCompressionRatio)) {
			return nil, fmt.Errorf("ZIP compression ratio exceeds limit")
		}
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, err
		}
		r, err := entry.Open()
		if err != nil {
			return nil, err
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			r.Close()
			return nil, err
		}
		hash := sha256.New()
		n, copyErr := io.Copy(io.MultiWriter(f, hash), io.LimitReader(r, limits.MaxFileBytes+1))
		closeErr := f.Close()
		r.Close()
		if copyErr != nil || closeErr != nil || n != int64(entry.UncompressedSize64) || n > limits.MaxFileBytes {
			return nil, fmt.Errorf("ZIP file is corrupt or too large")
		}
		total += n
		if total > limits.MaxExpandedBytes {
			return nil, fmt.Errorf("ZIP expanded size exceeds limit")
		}
		files[name] = FileInfo{Size: n, SHA256: hex.EncodeToString(hash.Sum(nil))}
	}
	if entry, ok := files["index.html"]; !ok || entry.Size == 0 {
		return nil, fmt.Errorf("ZIP must contain nonempty root index.html")
	}
	if err = os.Rename(stage, destination); err != nil {
		return nil, err
	}
	return files, nil
}

func WriteJSON(path string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0600)
}
