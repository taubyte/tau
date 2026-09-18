// Package splice carries bytes between two connections until both directions
// are finished.
//
// IT IS ITS OWN PACKAGE BECAUSE THE HALF-CLOSE RULE IS ONE RULE. Two copies of
// it would be two chances to make the mistake this package was extracted after:
// taking the first EOF as the end of the connection ends the half a query
// protocol is waiting on, so the request is delivered and the answer never
// comes — with no error anywhere, because a clean EOF is what the client reads.
package splice

import (
	"io"
	"net"
	"sync"
)

// Both copies a to b and b to a, and returns when both directions have ended.
//
// A HALF-CLOSE IS NOT A CLOSE for a protocol that uses one. A client that has
// finished sending and is waiting to be answered is the ordinary shape of a
// query — psql, redis, an HTTP request on a raw socket — so each direction ends
// on its own and the connection ends when both have.
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

// Shaper turns one chunk into what should be passed on. It is called with
// last=true once, when the source is done writing, so that a shaper holding
// bytes has somewhere to emit them.
//
// AN ERROR ENDS THE WHOLE CONNECTION AND NOT ONE DIRECTION. A shaper that
// refuses has decided about the conversation, not about the half of it that
// happened to be carrying bytes when it decided.
type Shaper func(chunk []byte, last bool) ([]byte, error)

// Through copies a to b and b to a as Both does, passing each chunk through the
// matching shaper first. A nil shaper is a plain copy of that direction.
//
// It returns the first error a shaper gave, or nil.
//
// # It is a chunk loop and not an io.Copy, which costs something
//
// Both hands the kernel two file descriptors and gets out of the way. This has
// to see every byte, so it reads into a buffer, calls out, and writes what
// comes back. That is the price of declaring a transformer on a proxy, and it
// is paid per chunk rather than per request — see the proxy-transformer world,
// which says the same thing from the other end.
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

			// A REFUSAL ENDS BOTH DIRECTIONS. Half-closing would leave the
			// other side waiting on a conversation something has already
			// decided about.
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

// chunkSize is how much is read at a time when a shaper is in the way.
//
// 32 KiB IS io.Copy'S OWN BUFFER, and matching it means a transformed stream
// makes the same number of syscalls as an untransformed one — the shaper is the
// cost, not the loop around it.
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
			// THE LAST CALL HAPPENS ON EVERY END, clean or not: a shaper
			// holding a partial frame has to be told the bytes are not coming.
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

// DoneWriting tells the far end that this direction is finished.
//
// THE FALLBACK IS A FULL CLOSE AND IT IS A REAL LOSS, so it is taken only when
// the connection genuinely cannot half-close. A wrapper that SAYS it can and
// does nothing is worse than one that admits it cannot: the far end waits for an
// EOF nobody sent.
func DoneWriting(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		if cw.CloseWrite() == nil {
			return
		}
	}
	c.Close()
}
