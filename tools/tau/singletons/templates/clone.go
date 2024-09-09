package templates

import (
	"errors"
	"os"
	"path"
	"strings"

	"github.com/taubyte/tau/pkg/git"
	"github.com/taubyte/tau/tools/tau/states"
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
	_, err := git.New(states.Context,
		git.Root(cloneFrom),
		git.URL(info.URL),
	)
	if err != nil {
		return "", err
	}

	return cloneFrom, nil
}

// CloneTo will create the directory for the template and move
// all files from the template repository except .git
func (info *TemplateInfo) CloneTo(dir string) error {
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
