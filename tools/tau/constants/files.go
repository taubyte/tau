package constants

import (
	"os"
	"path"
)

var (
	TauConfigFileName string
)

func init() {
	if e := os.Getenv("TAU_CONFIG_FILE"); e != "" {
		TauConfigFileName = e
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		panic("trying to find your home directory failed with:" + err.Error())
	}
	// `~/.tau/config.yaml`, AND IT USED TO BE `~/tau`. Three things were wrong
	// with that and only one of them was cosmetic.
	//
	// It is not the file anything reads. config.go derives the seer document
	// from the base name with `.yaml` trimmed, and seer is rooted at the
	// PARENT — so `~/tau` meant a seer over the whole home directory reading
	// `~/tau.yaml`, while load.go created an empty extensionless `~/tau` beside
	// it that nothing ever opened. Every tau user has that stray file.
	//
	// And it collides with a directory. `~/tau` is where people clone this
	// repository, and `os.Create` on a directory is EISDIR — which reaches the
	// user as a panic out of getOrCreateConfig, so the CLI does not start at
	// all for anyone who keeps the source there.
	//
	// A dot-directory with a real extension fixes all three: the file created
	// is the file read, nothing lands in the home listing, and a name nobody
	// would clone into cannot collide.
	TauConfigFileName = path.Join(home, ".tau", "config.yaml")
}
