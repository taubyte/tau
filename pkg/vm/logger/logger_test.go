package logger

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	corevm "github.com/taubyte/tau/core/vm"
)

// mockContext is a minimal implementation of corevm.Context for testing
type mockContext struct {
	ctx      context.Context
	project  string
	app      string
	resource string
}

func (m *mockContext) Context() context.Context { return m.ctx }
func (m *mockContext) Project() string          { return m.project }
func (m *mockContext) Application() string      { return m.app }
func (m *mockContext) Resource() string         { return m.resource }
func (m *mockContext) Branches() []string       { return nil }
func (m *mockContext) Commit() string           { return "" }
func (m *mockContext) Clone(c context.Context) corevm.Context {
	return &mockContext{ctx: c, project: m.project, app: m.app, resource: m.resource}
}

func newMockContext() corevm.Context {
	return &mockContext{
		ctx:      context.Background(),
		project:  "proj-abc",
		app:      "app-xyz",
		resource: "res-123",
	}
}

func TestLogger_WriteReadSingle(t *testing.T) {
	logDir := t.TempDir()

	l, err := New(logDir)
	if err != nil {
		t.Fatalf("New logger failed: %v", err)
	}
	t.Cleanup(func() { l.Close() })

	ctx := newMockContext()

	w, err := l.New(ctx)
	if err != nil {
		t.Fatalf("New writer failed: %v", err)
	}

	payload := []byte("hello world")
	if _, err := w.Write(payload); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}

	ts, err := l.Last(ctx)
	if err != nil {
		t.Fatalf("Last failed: %v", err)
	}

	r, err := l.Open(ctx, ts)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	t.Cleanup(func() { r.Close() })

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("unexpected payload: got %q want %q", string(got), string(payload))
	}
}

func TestLogger_EmptyCloseDoesNotWrite(t *testing.T) {
	logDir := t.TempDir()

	l, err := New(logDir)
	if err != nil {
		t.Fatalf("New logger failed: %v", err)
	}
	t.Cleanup(func() { l.Close() })

	ctx := newMockContext()

	w, err := l.New(ctx)
	if err != nil {
		t.Fatalf("New writer failed: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}

	// List should be empty
	tsList, err := l.List(ctx, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(tsList) != 0 {
		t.Fatalf("expected 0 timestamps, got %d", len(tsList))
	}
}

func TestLogger_MultipleWrites_ListOpen(t *testing.T) {
	logDir := t.TempDir()

	l, err := New(logDir)
	if err != nil {
		t.Fatalf("New logger failed: %v", err)
	}
	t.Cleanup(func() { l.Close() })

	ctx := newMockContext()

	// First entry
	w1, err := l.New(ctx)
	if err != nil {
		t.Fatalf("New writer 1 failed: %v", err)
	}
	if _, err := w1.Write([]byte("first")); err != nil {
		t.Fatalf("write 1 failed: %v", err)
	}
	if err := w1.Close(); err != nil {
		t.Fatalf("close 1 failed: %v", err)
	}

	// Ensure some time separation to avoid identical timestamps
	time.Sleep(1 * time.Millisecond)

	// Second entry
	w2, err := l.New(ctx)
	if err != nil {
		t.Fatalf("New writer 2 failed: %v", err)
	}
	if _, err := w2.Write([]byte("second")); err != nil {
		t.Fatalf("write 2 failed: %v", err)
	}
	if err := w2.Close(); err != nil {
		t.Fatalf("close 2 failed: %v", err)
	}

	tsList, err := l.List(ctx, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(tsList) != 2 {
		t.Fatalf("expected 2 timestamps, got %d", len(tsList))
	}
	if !tsList[0].Before(tsList[1]) && !tsList[0].Equal(tsList[1]) {
		t.Fatalf("timestamps not in chronological order")
	}

	// Open first
	r1, err := l.Open(ctx, tsList[0])
	if err != nil {
		t.Fatalf("Open first failed: %v", err)
	}
	b1, _ := io.ReadAll(r1)
	r1.Close()
	if string(b1) != "first" {
		t.Fatalf("unexpected first payload: %q", string(b1))
	}

	// Open second
	r2, err := l.Open(ctx, tsList[1])
	if err != nil {
		t.Fatalf("Open second failed: %v", err)
	}
	b2, _ := io.ReadAll(r2)
	r2.Close()
	if string(b2) != "second" {
		t.Fatalf("unexpected second payload: %q", string(b2))
	}
}

func TestLogger_NewInvalidDir(t *testing.T) {
	tmp := t.TempDir()
	// Create a file where directory is expected
	filePath := filepath.Join(tmp, "not_a_dir")
	if err := os.WriteFile(filePath, []byte("x"), 0644); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	if _, err := New(filePath); err == nil {
		t.Fatalf("expected error when creating logger with file path, got nil")
	}
}
