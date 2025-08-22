package readerutil

import (
	"bytes"
	"io"
	"io/ioutil"
	"os"
	"testing"
)

const text = "HelloWorld"

type testSrc struct {
	name string
	src  io.Reader
	want int64
}

func (tsrc *testSrc) run(t *testing.T) {
	n, ok := Size(tsrc.src)
	if !ok {
		t.Fatalf("failed to read size for %q", tsrc.name)
	}
	if n != tsrc.want {
		t.Fatalf("wanted %v, got %v", tsrc.want, n)
	}
}

func TestBytesBuffer(t *testing.T) {
	buf := bytes.NewBuffer([]byte(text))
	tsrc := &testSrc{"buffer", buf, int64(len(text))}
	tsrc.run(t)
}

func TestSeeker(t *testing.T) {
	f, err := ioutil.TempFile("", "camliTestReaderSize")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	defer f.Close()
	size, err := f.Write([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	pos, err := f.Seek(5, 0)
	if err != nil {
		t.Fatal(err)
	}
	tsrc := &testSrc{"seeker", f, int64(size) - pos}
	tsrc.run(t)
}
