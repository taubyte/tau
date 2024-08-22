package archive

import (
	"archive/zip"
	"bytes"
	"testing"
)

func TestNew(t *testing.T) {
	// Create a valid zip data
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	f, _ := zw.Create("module1")
	f.Write([]byte("content of module1"))
	zw.Close()
	data := buf.Bytes()

	archive, err := New(data)
	if err != nil {
		t.Errorf("New() error = %v, wantErr %v", err, false)
	}

	if archive == nil {
		t.Errorf("Expected archive to be non-nil")
	}
}

func TestNew_FailInvalidZip(t *testing.T) {
	_, err := New([]byte("not a zip"))
	if err == nil {
		t.Errorf("Expected error for invalid zip data, got nil")
	}
}

func TestMustNew(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("MustNew() should have panicked")
		}
	}()

	MustNew([]byte("invalid zip data"))
}

func TestModule(t *testing.T) {
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	f, _ := zw.Create("module1")
	f.Write([]byte("content of module1"))
	zw.Close()
	data := buf.Bytes()

	a, _ := New(data)
	content, err := a.Module("module1")
	if err != nil {
		t.Errorf("Module() error = %v, wantErr %v", err, false)
	}
	expected := "content of module1"
	if string(content) != expected {
		t.Errorf("Module() = %v, want %v", string(content), expected)
	}
}

func TestModule_NotFound(t *testing.T) {
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	zw.Close()
	data := buf.Bytes()

	a, _ := New(data)
	_, err := a.Module("nonexistent")
	if err == nil {
		t.Errorf("Expected error when module does not exist, got nil")
	}
}

func TestList(t *testing.T) {
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	zw.Create("module1")
	zw.Create("module2")
	zw.Close()
	data := buf.Bytes()

	a, _ := New(data)
	list := a.List()
	expected := 2
	if len(list) != expected {
		t.Errorf("List() length = %v, want %v", len(list), expected)
	}
}
