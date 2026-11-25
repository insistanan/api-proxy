package intent

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"insistanan/api-proxy/internal/storage/atomicfile"
)

func archiveName(name string) (string, error) {
	name = filepath.Clean(filepath.FromSlash(name))
	if name == "." || !filepath.IsLocal(name) || strings.Contains(name, ":") {
		return "", fmt.Errorf("运行时压缩包包含不安全路径")
	}
	return name, nil
}

// 支持官方 ZIP 和 tar.gz；内部动态库链接展开为普通文件，避免系统符号链接权限差异。
func extractRuntime(ctx context.Context, archivePath, dir, format, executable string) (string, error) {
	files, links := map[string]bool{}, map[string]string{}
	var total int64
	count := 0
	write := func(raw string, size int64, reader io.Reader) error {
		if strings.Contains(raw, "\\") {
			return fmt.Errorf("压缩包路径须使用正斜线")
		}
		name, err := archiveName(raw)
		if err != nil {
			return err
		}
		if files[name] || links[name] != "" {
			return fmt.Errorf("压缩包存在重复路径 %s", name)
		}
		count++
		if size < 0 || size > 128<<20 || total+size > 512<<20 || count > 512 {
			return fmt.Errorf("运行时压缩包文件数量或大小超限")
		}
		total += size
		err = atomicfile.WriteFunc(filepath.Join(dir, name), func(w io.Writer) error {
			n, err := io.Copy(w, io.LimitReader(contextReader{ctx, reader}, size+1))
			if err != nil {
				return err
			}
			if n != size {
				return fmt.Errorf("压缩包文件长度不匹配")
			}
			return nil
		}, atomicfile.Options{Mode: 0700})
		if err == nil {
			files[name] = true
		}
		return err
	}
	if format == "" || format == "zip" {
		reader, err := zip.OpenReader(archivePath)
		if err != nil {
			return "", fmt.Errorf("读取 ZIP 运行时失败: %w", err)
		}
		defer reader.Close()
		if len(reader.File) > 512 {
			return "", fmt.Errorf("压缩包条目过多")
		}
		for _, entry := range reader.File {
			if entry.Mode()&os.ModeSymlink != 0 {
				return "", fmt.Errorf("ZIP 不支持符号链接")
			}
			if entry.FileInfo().IsDir() {
				continue
			}
			if entry.UncompressedSize64 > 128<<20 {
				return "", fmt.Errorf("运行时文件超限")
			}
			file, err := entry.Open()
			if err != nil {
				return "", err
			}
			err = write(entry.Name, int64(entry.UncompressedSize64), file)
			if err = errors.Join(err, file.Close()); err != nil {
				return "", err
			}
		}
	} else if format == "tar.gz" {
		file, err := os.Open(archivePath)
		if err != nil {
			return "", err
		}
		defer file.Close()
		gzipReader, err := gzip.NewReader(file)
		if err != nil {
			return "", err
		}
		defer gzipReader.Close()
		reader := tar.NewReader(contextReader{ctx, gzipReader})
		entries := 0
		for {
			entry, err := reader.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return "", err
			}
			entries++
			if entries > 512 {
				return "", fmt.Errorf("压缩包条目过多")
			}
			switch entry.Typeflag {
			case tar.TypeDir:
				continue
			case tar.TypeReg:
				if err := write(entry.Name, entry.Size, reader); err != nil {
					return "", err
				}
			case tar.TypeSymlink, tar.TypeLink:
				if strings.ContainsAny(entry.Name+entry.Linkname, "\\:") || filepath.IsAbs(entry.Linkname) {
					return "", fmt.Errorf("压缩包链接路径无效")
				}
				name, err := archiveName(entry.Name)
				if err != nil {
					return "", err
				}
				target := entry.Linkname
				if entry.Typeflag == tar.TypeSymlink {
					target = filepath.Join(filepath.Dir(name), target)
				}
				target, err = archiveName(target)
				if err != nil || files[name] || links[name] != "" {
					return "", fmt.Errorf("压缩包链接越界或重复")
				}
				links[name] = target
			default:
				return "", fmt.Errorf("运行时压缩包包含不支持的文件类型")
			}
		}
		// tar 的 EOF 不保证已读到 gzip 尾部，继续有界读取以核对压缩流校验值。
		trailing, err := io.Copy(io.Discard, io.LimitReader(contextReader{ctx, gzipReader}, (1<<20)+1))
		if err != nil {
			return "", fmt.Errorf("运行时 gzip 校验失败: %w", err)
		}
		if trailing > 1<<20 {
			return "", fmt.Errorf("运行时压缩包尾部数据超限")
		}
	} else {
		return "", fmt.Errorf("运行时格式不支持")
	}
	for len(links) > 0 {
		resolved := false
		for name, target := range links {
			if !files[target] {
				continue
			}
			file, err := os.Open(filepath.Join(dir, target))
			if err != nil {
				return "", err
			}
			info, err := file.Stat()
			if err != nil {
				return "", errors.Join(err, file.Close())
			}
			delete(links, name)
			err = write(filepath.ToSlash(name), info.Size(), file)
			if err = errors.Join(err, file.Close()); err != nil {
				return "", err
			}
			resolved = true
		}
		if !resolved {
			return "", fmt.Errorf("运行时压缩包链接形成循环或指向不存在文件")
		}
	}
	executable = filepath.Clean(filepath.FromSlash(executable))
	if files[executable] {
		return executable, nil
	}
	if filepath.Base(executable) != executable {
		return "", fmt.Errorf("运行时未包含指定可执行文件 %s", executable)
	}
	found := ""
	for name := range files {
		if filepath.Base(name) == executable {
			if found != "" {
				return "", fmt.Errorf("运行时包含多个同名可执行文件，请指定相对路径")
			}
			found = name
		}
	}
	if found == "" {
		return "", fmt.Errorf("运行时未包含 %s", executable)
	}
	return found, nil
}
