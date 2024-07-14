package common

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"

	ci "github.com/taubyte/go-simple-container"
	"github.com/taubyte/tau/pkg/specs/builders"
	"github.com/taubyte/tau/pkg/specs/builders/wasm"
	"github.com/taubyte/tau/pkg/specs/builders/website"
	"github.com/taubyte/utils/bundle"
)

func Wd(workDir string) (*dir, error) {
	taubyteDir := TaubyteDir
	_, err := os.Stat(path.Join(workDir, TaubyteDir))
	if err != nil {
		taubyteDir = DepreciatedTaubyteDir
		_, err := os.Stat(path.Join(workDir, taubyteDir))
		if err != nil {
			return nil, fmt.Errorf("no taubyte directory found in `%s`", workDir)
		}
	}

	return &dir{
		wd:         workDir,
		taubyteDir: taubyteDir,
	}, nil
}

func (d *dir) Wasm() builders.Wasm {
	return wasm.Dir{Dir: d}
}

func (d *dir) Website() builders.Website {
	return website.Dir{Dir: d}
}

func (d *dir) String() string {
	return d.wd
}

func (d *dir) CodeSource(file string) string {
	return path.Join(d.String(), file)
}

func (d *dir) TaubyteDir() string {
	return path.Join(d.String(), d.taubyteDir)
}

func (d *dir) ConfigFile() string {
	return path.Join(d.TaubyteDir(), ConfigFile)
}

func (d *dir) DockerDir() builders.DockerDirType {
	return dockerDir(path.Join(d.TaubyteDir(), DockerDir))
}

func (d *dir) DockerFile() string {
	return path.Join(d.DockerDir().String(), Dockerfile)
}

func (d *dir) SetSourceVolume() ci.ContainerOption {
	return ci.Volume(d.String(), "/"+builders.Source)
}

func (d *dir) SetOutVolume(dir string) ci.ContainerOption {
	return ci.Volume(dir, "/"+builders.Output)
}

func (d *dir) SetEnvironmentVariables() ci.ContainerOption {
	return ci.Variables(map[string]string{
		"OUT": "/" + builders.Output,
		"SRC": "/" + builders.Source,
	})
}

func (d *dir) SetBuildCommand(script string) ci.ContainerOption {
	return ci.Command([]string{"/bin/sh", "/" + builders.Source + "/" + d.taubyteDir + "/" + script + ScriptExtension})
}

func (d *dir) DefaultOptions(script, outDir string, environment builders.Environment) []ci.ContainerOption {
	ops := []ci.ContainerOption{
		d.SetSourceVolume(),
		d.SetEnvironmentVariables(),
		d.SetOutVolume(outDir),
		d.SetBuildCommand(script),
	}

	if len(environment.Variables) > 0 {
		ops = append(ops, ci.Variables(environment.Variables))
	}

	return ops
}

func ExtraVolumes(wd string, volumes ...ExtraVolume) ([]ci.ContainerOption, error) {
	options := make([]ci.ContainerOption, 0, len(volumes))
	for _, volume := range volumes {
		if len(volume.SourcePath) == 0 || len(volume.ContainerPath) == 0 {
			return nil, errors.New("attempting to parse an empty volume")
		}

		if volume.SourceIsRelativeToBuildDirectory {
			volume.SourcePath = path.Join(wd, volume.SourcePath)
		}

		if string(volume.ContainerPath[0]) == "/" {
			options = append(options, ci.Volume(volume.SourcePath, volume.ContainerPath))
		} else {
			options = append(options, ci.Volume(volume.SourcePath, "/"+volume.ContainerPath))
		}
	}

	return options, nil
}

func (d dockerDir) String() string {
	return string(d)
}

func (d dockerDir) Stat() (fs.FileInfo, error) {
	fileInfo, err := os.Stat(string(d))
	if err != nil {
		return nil, err
	}
	if !fileInfo.IsDir() {
		return nil, fmt.Errorf("expected path `%s` to be directory, got file", d)
	}

	return fileInfo, nil
}
func (d dockerDir) Tar() ([]byte, error) {
	ops := bundle.Options{FileOptions: bundle.FileOptions{
		AccessTime: DefaultTime,
		ChangeTime: DefaultTime,
		ModTime:    DefaultTime,
	}}

	var buf bytes.Buffer
	err := bundle.Tarball(string(d), &ops, &buf)
	if err != nil {
		return nil, fmt.Errorf("tarball failed with: %s", err)
	}

	return buf.Bytes(), nil
}

func DefaultWDError(err error) error {
	return fmt.Errorf(defaultWDError, err)
}
