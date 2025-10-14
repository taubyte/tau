package bundle

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type ZipMethod func(archive *zip.Writer, source string, files ...string) error

// Zip contents from source to target and returns the file.
func Zip(zipMethod ZipMethod, source, target string, files ...string) (*os.File, error) {
	_, err := os.Stat(source)
	if err != nil {
		return nil, err
	}

	zipFile, err := os.Create(target)
	if err != nil {
		return nil, fmt.Errorf("create `%s`: %w", target, err)
	}

	defer zipFile.Seek(0, io.SeekStart)
	archive := zip.NewWriter(zipFile)
	defer archive.Close()

	if err = zipMethod(archive, source, files...); err != nil {
		return nil, err
	}

	return zipFile, archive.Flush()
}

func ZipFile(archive *zip.Writer, source string, files ...string) error {
	if len(files) != 1 {
		return fmt.Errorf("expected 1 file to zip, got %d", len(files))
	}

	data, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("reading path:`%s` failed with: %w", source, err)
	}

	writer, err := archive.Create(files[0])
	if err != nil {
		return err
	}

	if _, err = writer.Write(data); err != nil {
		return err
	}

	return nil
}

// REF: https://forum.golangbridge.org/t/trying-to-zip-files-without-creating-folder-inside-archive/10260
func ZipDir(archive *zip.Writer, source string, _ ...string) error {
	return filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return fmt.Errorf("walk basic source:`%s` failed with: %w", source, err)
		}

		if info.IsDir() {
			if source == path {
				return nil
			}
			path += "/"
		}

		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return fmt.Errorf("zipinfoheader `%s` failed with: %w", info, err)
		}

		header.Name = path[len(source)+1:]
		header.Method = zip.Deflate

		writer, err := archive.CreateHeader(header)
		if err != nil {
			return fmt.Errorf("archive create header failed with: %w", err)
		}

		if info.IsDir() {
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("open Path:`%s` failed with: %w", path, err)
		}
		defer file.Close()

		_, err = io.Copy(writer, file)
		if err != nil {
			return fmt.Errorf("copy failed with: %w", err)
		}

		return nil
	})
}
