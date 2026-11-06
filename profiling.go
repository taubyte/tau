package main

import (
	"log"
	"net/http"
	_ "net/http/pprof" // registers the /debug/pprof handlers on http.DefaultServeMux
	"os"
)

// startProfiling serves Go's runtime profiles when TAU_PPROF_ADDR is set, for
// diagnosing a running node (memory growth, stuck goroutines). It is off by
// default; bind it to a loopback address, the profiles describe the process.
func startProfiling() {
	addr := os.Getenv("TAU_PPROF_ADDR")
	if addr == "" {
		return
	}
	go func() {
		if err := http.ListenAndServe(addr, nil); err != nil {
			log.Printf("profiling on %s stopped: %v", addr, err)
		}
	}()
}
