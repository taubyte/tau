package common

type dir struct {
	wd         string
	taubyteDir string
}

type dockerDir string

type ExtraVolume struct {
	SourcePath                       string
	ContainerPath                    string
	SourceIsRelativeToBuildDirectory bool
}
