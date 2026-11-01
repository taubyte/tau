// Package splice carries bytes between two connections until both directions are
// finished. Taking the first EOF as the end would cut off the answer a query protocol
// is waiting for, with no error anywhere.
package splice

import (
	"io"
	"net"
	"sync"
)

// Both copies a to b and b to a, and returns when both directions have ended. A
// half-close is not a close: a client done sending and waiting for its answer is the
// ordinary shape of a query.
func Both(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)

	half := func(dst, src net.Conn) {
		defer wg.Done()
		io.Copy(dst, src)
		DoneWriting(dst)
	}

	go half(a, b)
	go half(b, a)
	wg.Wait()
}

// Shaper turns one chunk into what should be passed on. It is called with last=true
// once, when the source is done writing, so a shaper holding bytes can emit them. An
// error ends the whole connection, not one direction.
type Shaper func(chunk []byte, last bool) ([]byte, error)

// Through copies a to b and b to a as Both does, passing each chunk through the
// matching shaper first. A nil shaper is a plain copy of that direction. It returns
// the first error a shaper gave, or nil. Seeing every byte costs a buffered chunk
// loop rather than a kernel copy.
func Through(a, b net.Conn, aToB, bToA Shaper) error {
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		fail error
	)
	wg.Add(2)

	half := func(dst, src net.Conn, shape Shaper) {
		defer wg.Done()
		if err := shaped(dst, src, shape); err != nil {
			mu.Lock()
			if fail == nil {
				fail = err
			}
			mu.Unlock()

			// A refusal ends both directions.
			a.Close()
			b.Close()
			return
		}
		DoneWriting(dst)
	}

	go half(a, b, bToA)
	go half(b, a, aToB)
	wg.Wait()

	return fail
}

// chunkSize matches io.Copy's buffer, so a shaped stream makes the same syscalls.
const chunkSize = 32 * 1024

func shaped(dst, src net.Conn, shape Shaper) error {
	if shape == nil {
		io.Copy(dst, src)
		return nil
	}

	buf := make([]byte, chunkSize)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			out, shapeErr := shape(buf[:n], false)
			if shapeErr != nil {
				return shapeErr
			}
			if len(out) > 0 {
				if _, writeErr := dst.Write(out); writeErr != nil {
					return nil // the far end went away; that is not a refusal
				}
			}
		}
		if err != nil {
			// The last call happens on every end, clean or not, so a shaper holding a partial
			// frame learns the bytes are not coming.
			out, shapeErr := shape(nil, true)
			if shapeErr != nil {
				return shapeErr
			}
			if len(out) > 0 {
				dst.Write(out)
			}
			return nil
		}
	}
}

// DoneWriting tells the far end that this direction is finished, falling back to a
// full close only when the connection cannot half-close.
func DoneWriting(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		if cw.CloseWrite() == nil {
			return
		}
	}
	c.Close()
}
