package tests

import (
	"context"
	"net/http/httptest"
	"testing"
)

// The fifo plugin shares an in-memory fifo across modules in one runtime: a
// "writer" module creates a fifo and writes to it, a "reader" module imports
// the writer's fifo_1 export, calls it to get the id, then reads the fifo back.
// These tests cross the Go/Rust boundary in both directions.

func fifoCrossTest(t *testing.T, readerModule, readerWasm, writerWasm string) {
	req := httptest.NewRequest("GET", "/fifo", nil)
	w, _ := guestCallMulti(t, context.Background(),
		map[string]string{
			readerModule:  readerWasm, // entry: reads the fifo
			"fifo_writer": writerWasm, // dependency: fifo_1 writes the fifo
		},
		readerModule, "fifo_2", req, testCtxOpts()...)

	if got := w.Body.String(); got != "hello world" {
		t.Fatalf("body = %q, want %q", got, "hello world")
	}
}

// Go writes the fifo, Rust reads it.
func TestFifoGoToRust(t *testing.T) {
	fifoCrossTest(t, "fifo_mod_2_rs", "fifo_mod_2_rs", "fifo_mod_1")
}

// Rust writes the fifo, Go reads it.
func TestFifoRustToGo(t *testing.T) {
	fifoCrossTest(t, "fifo_mod_2", "fifo_mod_2", "fifo_mod_1_rs")
}
