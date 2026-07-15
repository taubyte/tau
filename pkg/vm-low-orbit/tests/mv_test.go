package tests

import (
	"context"
	"net/http/httptest"
	"testing"
)

// memoryView is the same cross-module pattern as fifo: a writer module creates
// a memory view, a reader module imports its mv_1 export and reads it back.
func mvCrossTest(t *testing.T, readerModule, readerWasm, writerWasm string) {
	req := httptest.NewRequest("GET", "/mv", nil)
	w, _ := guestCallMulti(t, context.Background(),
		map[string]string{
			readerModule: readerWasm,
			"mv_writer":  writerWasm,
		},
		readerModule, "mv_2", req, testCtxOpts()...)

	if got := w.Body.String(); got != "hello world" {
		t.Fatalf("body = %q, want %q", got, "hello world")
	}
}

func TestMemoryViewGoToRust(t *testing.T) {
	mvCrossTest(t, "mv_mod_2_rs", "mv_mod_2_rs", "mv_mod_1")
}

func TestMemoryViewRustToGo(t *testing.T) {
	mvCrossTest(t, "mv_mod_2", "mv_mod_2", "mv_mod_1_rs")
}
