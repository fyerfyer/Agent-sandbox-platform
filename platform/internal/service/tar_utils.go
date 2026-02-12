package service

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// extractTarToDir 将 tar 流解压到目标目录
func extractTarToDir(r io.Reader, destDir string) error {
	tr := tar.NewReader(r)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("tar read error: %w", err)
		}

		cleanName := filepath.Clean(header.Name)
		if strings.HasPrefix(cleanName, "..") {
			continue
		}

		parts := strings.SplitN(cleanName, string(os.PathSeparator), 2)
		if len(parts) < 2 {
			continue
		}
		relPath := parts[1]
		target := filepath.Join(destDir, relPath)

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(header.Mode)); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		}
	}
}

// extractFirstFileFromTar 读取 tar 流并将第一个文件写入 w
func extractFirstFileFromTar(r io.Reader, w io.Writer) error {
	tr := tar.NewReader(r)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("no file found in archive")
		}
		if err != nil {
			return fmt.Errorf("tar read error: %w", err)
		}
		if header.Typeflag == tar.TypeReg {
			_, err := io.Copy(w, tr)
			return err
		}
	}
}
