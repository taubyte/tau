package service

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"

	"github.com/spf13/afero"
)

func zipFilesystem(ctx context.Context, fs afero.Fs, writer io.Writer) error {
	zipWriter := zip.NewWriter(writer)
	defer zipWriter.Close()

	err := afero.Walk(fs, "/", func(p string, info os.FileInfo, err error) error {
		select {
		case <-ctx.Done():
			return nil
		default:
			if err != nil {
				return fmt.Errorf("failed to access path %s: %w", p, err)
			}

			absPath := path.Clean("/" + filepath.ToSlash(p))

			if absPath == "/" {
				return nil
			}

			if info.IsDir() {
				_, err := zipWriter.Create(absPath + "/")
				if err != nil {
					return fmt.Errorf("failed to add directory to zip: %w", err)
				}
			} else {
				file, err := fs.Open(p)
				if err != nil {
					return fmt.Errorf("failed to open file %s: %w", p, err)
				}
				defer file.Close()

				zipFileWriter, err := zipWriter.Create(absPath)
				if err != nil {
					return fmt.Errorf("failed to create zip entry for file %s: %w", p, err)
				}

				_, err = io.Copy(zipFileWriter, file)
				if err != nil {
					return fmt.Errorf("failed to copy file content for %s: %w", p, err)
				}
			}

			return nil
		}
	})

	if err != nil {
		return fmt.Errorf("error walking through afero.Fs: %w", err)
	}

	return nil
}
