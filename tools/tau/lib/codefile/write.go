package codefile

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/taubyte/tau/pkg/cli/common"
)

func (p CodePath) Write(templateURL, nameForMd string) error {
	templateURL = filepath.ToSlash(templateURL)

	err := os.MkdirAll(p.String(), common.DefaultDirPermission)
	if err != nil {
		return err
	}

	toWrite := make(map[string][]byte)
	if len(templateURL) > 0 {
		var err0 error
		err := filepath.WalkDir(templateURL, func(path string, d fs.DirEntry, err error) error {
			if d.Name() != "config.yaml" && !d.IsDir() {
				toWrite[d.Name()], err0 = os.ReadFile(path)
				if err0 != nil {
					return err0
				}
			}
			return nil
		})
		if err != nil {
			return err
		}

		split := strings.Split(templateURL, "/")
		templateCommon := getTemplateCommon(split)
		if _, err := os.Stat(templateCommon); err == nil {
			var err0 error
			err = filepath.WalkDir(templateCommon, func(_path string, d fs.DirEntry, err error) error {
				_path = filepath.ToSlash(_path)
				if d.IsDir() {
					if d.Name() != "common" {
						err0 = os.MkdirAll(path.Join(p.String(), d.Name()), common.DefaultDirPermission)
						if err0 != nil {
							return err0
						}
					}
				} else {
					toWrite[strings.TrimPrefix(_path, templateCommon+"/")], err0 = os.ReadFile(_path)
					if err0 != nil {
						return err0
					}
				}
				return nil
			})
			if err != nil {
				return err
			}
		}

		for name, data := range toWrite {
			err = os.WriteFile(path.Join(p.String(), name), data, common.DefaultFilePermission)
			if err != nil {
				return err
			}
		}
	} else {
		err := os.WriteFile(path.Join(p.String(), nameForMd+".md"), nil, common.DefaultFilePermission)
		if err != nil {
			return err
		}
	}

	return nil
}
