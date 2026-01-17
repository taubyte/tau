package dir

import (
	"errors"
	"os"
	"path/filepath"
)

type Directory string

// New builds an absolute path from the provided path, makes the required directories to achieve it, and returns a Directory
func New(path string) (Directory, error) {
	_path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return Directory(_path), os.MkdirAll(path, 0755)
}

// Open builds an absolute path from the provided path, confirms the path exists, and returns a Directory
func Open(path string) (Directory, error) {
	_path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	_, err = os.Stat(_path)
	return Directory(_path), err
}

// Path gets the absolute path of the directory
func (d Directory) Path() string {
	return string(d)
}

// Remove removes the directory and any children within it
func (d Directory) Remove() error {
	if d == "" {
		return errors.New("Can't remove empty Directory")
	}
	return os.RemoveAll(d.Path())
}

// Move moves the directory to the provided path, returns a directory of the new path
func (d Directory) Move(to string) (Directory, error) {
	_path, err := filepath.Abs(to)
	if err != nil {
		return "", err
	}
	err = os.Rename(d.Path(), _path)
	if err != nil {
		return "", err
	}

	return Directory(_path), nil
}
