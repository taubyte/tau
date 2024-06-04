package fixtures

import "github.com/spf13/afero"

func writeFixture(fs afero.Fs, folder string, toWrite map[string]map[string]string) (afero.Fs, error) {
	var root string
	var f afero.File
	var err error
	for name, data := range toWrite {
		application := data["application"]
		if len(application) > 0 {
			root = rootDir + "applications/" + application + "/" + folder
			f, err = fs.Create(root + "/" + name + ".yaml")
			if err != nil {
				return nil, err
			}
		} else {
			root = rootDir + folder
			f, err = fs.Create(root + "/" + name + ".yaml")
			if err != nil {
				return nil, err
			}
		}
		_, err = f.WriteString(data["write"])
		if err != nil {
			return nil, err
		}

		if f.Close() != nil {
			return nil, err
		}
	}

	return fs, nil
}
