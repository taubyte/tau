package readerutil

import (
	"os"
	"strings"
	"testing"
)

func TestFakeSeeker(t *testing.T) {
	rs := NewFakeSeeker(strings.NewReader("foobar"), 6)
	if pos, err := rs.Seek(0, os.SEEK_END); err != nil || pos != 6 {
		t.Fatalf("SEEK_END = %d, %v; want 6, nil", pos, err)
	}
	if pos, err := rs.Seek(0, os.SEEK_CUR); err != nil || pos != 6 {
		t.Fatalf("SEEK_CUR = %d, %v; want 6, nil", pos, err)
	}
	if pos, err := rs.Seek(0, os.SEEK_SET); err != nil || pos != 0 {
		t.Fatalf("SEEK_SET = %d, %v; want 0, nil", pos, err)
	}

	buf := make([]byte, 3)
	if n, err := rs.Read(buf); n != 3 || err != nil || string(buf) != "foo" {
		t.Fatalf("First read = %d, %v (buf = %q); want foo", n, err, buf)
	}
	if pos, err := rs.Seek(0, os.SEEK_CUR); err != nil || pos != 3 {
		t.Fatalf("Seek cur pos after first read = %d, %v; want 3, nil", pos, err)
	}
	if n, err := rs.Read(buf); n != 3 || err != nil || string(buf) != "bar" {
		t.Fatalf("Second read = %d, %v (buf = %q); want foo", n, err, buf)
	}

	if pos, err := rs.Seek(1, os.SEEK_SET); err != nil || pos != 1 {
		t.Fatalf("SEEK_SET = %d, %v; want 1, nil", pos, err)
	}
	const msg = "attempt to read from fake seek offset"
	if _, err := rs.Read(buf); err == nil || !strings.Contains(err.Error(), msg) {
		t.Fatalf("bogus Read after seek = %v; want something containing %q", err, msg)
	}
}
