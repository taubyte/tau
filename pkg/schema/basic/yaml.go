package basic

import (
	"github.com/spf13/afero"
	"github.com/taubyte/go-seer"
)

type yamlIface struct {
	ResourceIface
}

func Yaml(data []byte) (*Resource, error) {
	fs := afero.NewMemMapFs()

	file, err := fs.Create("/config.yaml")
	if err != nil {
		return nil, err
	}
	defer file.Close()

	_, err = file.Write(data)
	if err != nil {
		return nil, err
	}

	_seer, err := seer.New(seer.VirtualFS(fs, "/"))
	if err != nil {
		return nil, err
	}

	resource := &Resource{
		ResourceIface: yamlIface{},
		seer:          _seer,
		Root: func() *seer.Query {
			return _seer.Get("config")
		},
		Config: func() *seer.Query {
			return _seer.Get("config").Document()
		},
	}

	return resource, err
}
