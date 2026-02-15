package worker

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func TarContext(srcPath string) (io.Reader, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	absSrc, err := filepath.Abs(srcPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}

	err = filepath.Walk(absSrc, func(file string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// 处理符号链接
		var link string
		if fi.Mode()&os.ModeSymlink != 0 {
			link, err = os.Readlink(file)
			if err != nil {
				return fmt.Errorf("failed to read symlink: %w", err)
			}
		}

		header, err := tar.FileInfoHeader(fi, link)
		if err != nil {
			return fmt.Errorf("failed to create header: %w", err)
		}

		relPath, err := filepath.Rel(absSrc, file)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(relPath)

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		// 只处理普通文件
		if !fi.Mode().IsRegular() {
			return nil
		}

		// 避免 defer 堆积
		if err := writeFileToTar(tw, file); err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		tw.Close() // 出错时也要关闭
		return nil, fmt.Errorf("failed to create tar archive: %w", err)
	}

	// 显式关闭并检查错误
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("failed to finalize tar archive: %w", err)
	}

	return &buf, nil
}

// defer 在函数返回时立即执行
func writeFileToTar(tw *tar.Writer, filePath string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close() // 这里的 defer 在每个文件处理完就执行

	_, err = io.Copy(tw, f)
	return err
}

func GenerateEnvFile(envVars []string) io.Reader {
	var sb strings.Builder
	for _, env := range envVars {
		sb.WriteString(env)
		sb.WriteString("\n")
	}
	return strings.NewReader(sb.String())
}

// ensureDir 确保目录存在，如果不存在则创建（包括父目录）。
func ensureDir(path string) error {
	return os.MkdirAll(path, 0755)
}
