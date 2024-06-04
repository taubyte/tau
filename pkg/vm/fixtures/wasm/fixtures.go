package fixtures

import (
	"bytes"
	"compress/lzw"
	_ "embed"
	"io"
)

var (
	//go:embed recursive.wasm
	NonCompressRecursive []byte // non compressed

	//go:embed artifact.zwasm
	Artifact []byte
)

var Recursive []byte // compressed
var ArtifactNonCompress []byte

func init() {
	buf := bytes.NewBuffer(nil)
	sbuf := bytes.NewBuffer(NonCompressRecursive)
	w := lzw.NewWriter(buf, lzw.LSB, 8)
	io.Copy(w, sbuf)
	w.Close()
	Recursive = buf.Bytes()

	aReader := lzw.NewReader(bytes.NewReader(Artifact), lzw.LSB, 8)

	var err error
	ArtifactNonCompress, err = io.ReadAll(aReader)
	if err != nil {
		panic(err)
	}
}
