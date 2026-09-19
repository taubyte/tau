package templates

import (
	"context"
	"errors"
	"os"
	"path"
	"strings"

	"github.com/taubyte/tau/pkg/git"
)

// Template root will give the location of the template and clone
// if needed, ex /tmp/taubyte_templates/websites/tb_angular_template
func (info *TemplateInfo) templateRoot() (string, error) {
	if len(info.URL) == 0 {
		return "", errors.New("template URL not set")
	}

	// split on / and get final item for the name
	splitName := strings.Split(info.URL, "/")
	cloneFrom := path.Join(templateFolder, splitName[len(splitName)-1])

	// open or clone the repository
	ctx, cancel := context.WithTimeout(context.Background(), defaultTemplateGitTimeout)
	defer cancel()
	if err := openOrReclone(ctx, cloneFrom, info.URL); err != nil {
		return "", err
	}

	return cloneFrom, nil
}

// openOrReclone opens the cached clone, and replaces it when it cannot.
//
// THE CACHE IS IN /tmp AND SURVIVES THE RUN THAT BROKE IT. A pull that died
// halfway leaves a directory that is not a repository, and every run after it
// fails on whatever file the interruption happened to miss — a different error
// each time, none of them about the code. What cannot be opened is thrown away
// and fetched again, which is the only thing a cache of something immutable
// owes anybody.
func openOrReclone(ctx context.Context, root, url string) error {
	_, err := git.New(ctx, git.Root(root), git.URL(url))
	if err == nil {
		return nil
	}

	if rmErr := os.RemoveAll(root); rmErr != nil {
		return err
	}

	_, err = git.New(ctx, git.Root(root), git.URL(url))
	return err
}

// CloneTo will create the directory for the template and move
// all files from the template repository except .git
func (info *TemplateInfo) CloneTo(dir string) error {
	// THE COMMENT ABOVE SAYS THIS CREATES THE DIRECTORY, and it did not: every
	// caller had to make it first, and one that did not got `no such file or
	// directory` from the first file copied — which reads as a missing template
	// rather than a missing destination.
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	root, err := info.templateRoot()
	if err != nil {
		return err
	}

	// copy all files from root to dir except `.git`
	err = copyFiles(root, dir, func(f os.DirEntry) bool {
		return f.Name() != ".git"
	})
	if err != nil {
		return err
	}

	return nil
}

func copyFiles(src, dst string, filter func(f os.DirEntry) bool) error {
	files, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, f := range files {
		if !filter(f) {
			continue
		}

		srcFile := path.Join(src, f.Name())
		dstFile := path.Join(dst, f.Name())

		if f.IsDir() {
			err = os.MkdirAll(dstFile, 0755)
			if err != nil {
				return err
			}

			err = copyFiles(srcFile, dstFile, filter)
			if err != nil {
				return err
			}
		} else {
			err = copyFile(srcFile, dstFile)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func copyFile(src string, dst string) error {
	srcData, err := os.ReadFile(src)
	if err != nil {
		return err
	}

	return os.WriteFile(dst, srcData, 0755)
}
