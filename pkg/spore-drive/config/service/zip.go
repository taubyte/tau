package service

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/afero"
)

func zipFilesystem(fs afero.Fs, writer io.Writer) error {
	zipWriter := zip.NewWriter(writer)
	defer zipWriter.Close()

	err := afero.Walk(fs, "/", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return fmt.Errorf("failed to access path %s: %w", path, err)
		}

		relPath := filepath.ToSlash(strings.TrimPrefix(path, "/"))

		if relPath == "" {
			return nil
		}

		if info.IsDir() {
			_, err := zipWriter.Create(relPath + "/")
			if err != nil {
				return fmt.Errorf("failed to add directory to zip: %w", err)
			}
		} else {
			file, err := fs.Open(path)
			if err != nil {
				return fmt.Errorf("failed to open file %s: %w", path, err)
			}
			defer file.Close()

			zipFileWriter, err := zipWriter.Create(relPath)
			if err != nil {
				return fmt.Errorf("failed to create zip entry for file %s: %w", path, err)
			}

			_, err = io.Copy(zipFileWriter, file)
			if err != nil {
				return fmt.Errorf("failed to copy file content for %s: %w", path, err)
			}
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("error walking through afero.Fs: %w", err)
	}

	return nil
}
