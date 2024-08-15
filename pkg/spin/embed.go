package spin

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"fmt"
	"io"
	"sync"

	"go4.org/readerutil"
)

var (
	//go:embed assets/runtimes.zip
	runtimesData []byte

	//go:embed assets/tools.zip
	//toolsData []byte

	runtimeADM64   []byte
	runtimeRISCV64 []byte

	runtimeLock sync.RWMutex
)

func readZipFile(f *zip.File) ([]byte, error) {
	zipFileReader, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer zipFileReader.Close()

	buf, err := io.ReadAll(zipFileReader)
	if err != nil {
		return nil, err
	}

	return buf, nil
}

func extractRuntimes() error {
	runtimeLock.Lock()
	defer runtimeLock.Unlock()

	// another reuqest did it already
	if runtimeADM64 != nil && runtimeRISCV64 != nil {
		return nil
	}

	zipReader, err := zip.NewReader(
		readerutil.NewBufferingReaderAt(bytes.NewBuffer(runtimesData)),
		int64(len(runtimesData)),
	)
	if err != nil {
		return err
	}

	for _, file := range zipReader.File {
		fmt.Println(file.Name)
		var err error
		switch file.Name {
		case "amd64.wasm":
			runtimeADM64, err = readZipFile(file)
		case "riscv64.wasm":
			runtimeRISCV64, err = readZipFile(file)
		}
		if err != nil {
			return err
		}
	}

	return nil
}

func RuntimeADM64() ([]byte, error) {
	var data []byte
	runtimeLock.RLock()
	data = runtimeADM64
	runtimeLock.RUnlock()
	if data == nil {
		extractRuntimes()
		runtimeLock.RLock()
		data = runtimeADM64
		runtimeLock.RUnlock()
	}
	return data, nil
}

func RuntimeRISCV64() ([]byte, error) {
	var data []byte
	runtimeLock.RLock()
	data = runtimeRISCV64
	runtimeLock.RUnlock()
	if data == nil {
		extractRuntimes()
		runtimeLock.RLock()
		data = runtimeRISCV64
		runtimeLock.RUnlock()
	}
	return data, nil
}

func init() {
	if err := extractRuntimes(); err != nil {
		panic(err)
	}
}
