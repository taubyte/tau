package main

import (
	"bufio"
	"errors"
	"net"
	"net/url"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestMockEndsWhenItsStdinCloses(t *testing.T) {
	stdin, hold, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	out, stdout, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	in0, out0 := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = stdin, stdout
	t.Cleanup(func() {
		os.Stdin, os.Stdout = in0, out0
		for _, f := range []*os.File{stdin, hold, out, stdout} {
			f.Close()
		}
	})

	done := make(chan struct{})
	go func() {
		main()
		close(done)
	}()

	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(strings.TrimSpace(line))
	if err != nil {
		t.Fatal(err)
	}
	addr := net.JoinHostPort("localhost", u.Port())
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("the mock does not answer at %s: %v", addr, err)
	}
	c.Close()

	// What killing the runner outright does to the pipe it holds.
	hold.Close()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the mock still runs 10s after its stdin closed")
	}

	c, err = net.Dial("tcp", addr)
	if err == nil {
		c.Close()
		t.Fatal("the mock still answers after its stdin closed")
	}
	if !errors.Is(err, syscall.ECONNREFUSED) {
		t.Fatalf("dialing a stopped mock: %v, want connection refused", err)
	}
}
