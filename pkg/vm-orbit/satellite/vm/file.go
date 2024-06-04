package vm

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/multiformats/go-multihash"
)

func (v *vmPlugin) prepFile() error {
	f, err := os.Open(v.origin)
	if err != nil {
		return fmt.Errorf("opening file %s failed with: %w", v.origin, err)
	}

	defer f.Close()

	name := filepath.Base(v.origin)

	file, err := os.CreateTemp("/tmp", name+"-")
	if err != nil {
		return fmt.Errorf("creating temp file `%s` failed with: %w", name, err)
	}

	if err = file.Chmod(0755); err != nil {
		return fmt.Errorf("chmod 0755 on `%s` failed with: %w", file.Name(), err)
	}

	defer file.Close()
	if _, err := io.Copy(file, f); err != nil {
		return fmt.Errorf("copying from `%s` to `%s` failed with: %w", f.Name(), file.Name(), err)
	}

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seeking start in file `%s` failed with: %w", f.Name(), err)
	}

	v.filename = file.Name()
	return nil
}

func (v *vmPlugin) hashFile() error {
	f, err := os.Open(v.origin)
	if err != nil {
		return fmt.Errorf("opening file `%s` failed with: %w", v.origin, err)
	}

	defer f.Close()

	mh, err := multihash.SumStream(f, multihash.SHA2_256, -1)
	if err != nil {
		return fmt.Errorf("multi-hashing `%s` failed with: %w", f.Name(), err)
	}

	if err = os.WriteFile(fmt.Sprintf("%s.hash", v.origin), []byte(mh.B58String()), 0644); err != nil {
		return fmt.Errorf("writing to `%s.hash` failed with: %w", v.origin, err)
	}

	return nil
}
