package service

import (
	"archive/tar"
	"context"
	"io"
	"os"

	"github.com/spf13/afero"
)

func tarFilesystem(ctx context.Context, fs afero.Fs, dest io.Writer) error {
	tarWriter := tar.NewWriter(dest)
	defer tarWriter.Close()

	return afero.Walk(fs, "/", func(path string, info os.FileInfo, err error) error {
		select {
		case <-ctx.Done():
			return nil
		default:
			if err != nil {
				return err
			}

			header, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return err
			}

			header.Name = path

			if err := tarWriter.WriteHeader(header); err != nil {
				return err
			}

			if info.IsDir() {
				return nil
			}

			file, err := fs.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()

			_, err = io.Copy(tarWriter, file)
			return err
		}
	})
}
