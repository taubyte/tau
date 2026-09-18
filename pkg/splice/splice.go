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
