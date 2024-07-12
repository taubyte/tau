package utils

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// CopyFile copies a file from src to dst. If dst does not exist, it is created.
// If it exists, its contents are replaced with the contents of src.
func CopyFile(src, dst string) error {
	// Open the source file
	sourceFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("could not open source file: %v", err)
	}
	defer sourceFile.Close()

	// Create the destination file
	destFile, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("could not create destination file: %v", err)
	}
	defer destFile.Close()

	// Copy the contents from the source file to the destination file
	_, err = io.Copy(destFile, sourceFile)
	if err != nil {
		return fmt.Errorf("could not copy file contents: %v", err)
	}

	// Ensure the copied file's contents are flushed to storage
	err = destFile.Sync()
	if err != nil {
		return fmt.Errorf("could not sync destination file: %v", err)
	}

	return nil
}

// CopyDir copies all files from srcDir to dstDir, skipping existing files in dstDir.
func CopyDir(srcDir, dstDir string) error {
	// Ensure the destination directory exists
	err := os.MkdirAll(dstDir, os.ModePerm)
	if err != nil {
		return fmt.Errorf("could not create destination directory: %v", err)
	}

	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil // Skip directories
		}

		// Calculate the relative path from the source directory
		relPath, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}

		// Determine the destination file path
		destPath := filepath.Join(dstDir, relPath)

		// Skip the file if it already exists in the destination directory
		if _, err := os.Stat(destPath); err == nil {
			fmt.Printf("Skipping existing file: %s\n", destPath)
			return nil
		}

		// Create the destination directory if it does not exist
		destDirPath := filepath.Dir(destPath)
		err = os.MkdirAll(destDirPath, os.ModePerm)
		if err != nil {
			return fmt.Errorf("could not create destination directory: %v", err)
		}

		// Copy the file
		err = CopyFile(path, destPath)
		if err != nil {
			return fmt.Errorf("could not copy file: %v", err)
		}

		return nil
	})
}

func SafeAbs(p string) string {
	ap, _ := filepath.Abs(p)
	return ap
}
