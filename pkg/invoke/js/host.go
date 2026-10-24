package js

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/textproto"
	"strings"
	"unicode/utf8"

	"warpjs/engine"

	"github.com/taubyte/tau/pkg/invoke"
)

// host.go is what the prelude reaches in Go: the request and response, the
// console's sink, and UTF-8 in both directions, which a JS loop over every
// byte would make the slowest part of a body.
//
// # Bytes cross through one window, never as a Value
//
// Every JS value that reaches Go as an engine.Value is pinned until Go's
// collector runs the pin's cleanup, however soon Go drops it. A body handed
// over as a Value stays in the arena until then, and on a node whose Go heap
// is large that is thousands of requests later: a 16 MiB realm serving 128 KiB
// responses ran out of arena with nothing in it live. So every function here
// is typed (engine.Fn*), whose strings and numbers convert without a Value, and
// bytes go through the window: one ArrayBuffer per realm, pinned once, that
// both sides copy into and out of. The prelude grows it, and the grown one
// replaces it.
//
// THE WINDOW IS ONLY EVER USED WITHIN ONE SYNCHRONOUS STRETCH: written and read
// with no JS in between, so a promise reaction cannot overwrite bytes the other
// side has not read yet.

var errWindow = errors.New("js: the prelude's window does not hold what it was told")

func (i *instance) hostObject(stdout, stderr io.Writer) (engine.Value, error) {
	host := i.rt.NewObject()
	for _, f := range []struct {
		name string
		fn   engine.HostFunction
	}{
		{"window", engine.Fn1(func(buf engine.Value) bool {
			i.window = buf
			return true
		})},
		{"head", engine.Fn0(i.head)},
		{"respond", engine.Fn3E(i.respond)},
		{"respondText", engine.Fn3(i.respondText)},
		{"encode", engine.Fn1(i.encode)},
		{"decode", engine.Fn3E(i.decode)},
		{"log", engine.Fn2(func(level int, text string) bool {
			w := stdout
			if level != 0 {
				w = stderr
			}
			if w != nil {
				// A sink that cannot be written to does not fail the request
				// that logged.
				io.WriteString(w, text+"\n")
			}
			return true
		})},
	} {
		if err := host.Set(f.name, f.fn); err != nil {
			return engine.Value{}, err
		}
	}
	return host, nil
}

// view is the window's bytes. It aliases the arena until the next allocation,
// which none of its callers make while they hold it.
func (i *instance) view(n int) ([]byte, error) {
	view, ok := i.rt.Bytes(i.window)
	if !ok || n < 0 || n > len(view) {
		return nil, fmt.Errorf("%w: %d bytes", errWindow, n)
	}
	return view[:n], nil
}

// head is the request being served: its method, URL, header pairs, and the
// length of the body handle has already put in the window.
func (i *instance) head() []any {
	req := i.current
	method := req.Method
	if method == "" {
		method = defaultMethod
	}
	target := req.Path
	if !strings.HasPrefix(target, "/") {
		target = "/" + target
	}
	return []any{method, origin + target, pairs(req.Headers), len(req.Body)}
}

// respond takes the handler's answer: the status, the header pairs and the
// length of the body in the window, -1 for none.
func (i *instance) respond(status int, flat []string, n int) (bool, error) {
	resp := answer(status, flat)
	if n >= 0 {
		body, err := i.view(n)
		if err != nil {
			return false, err
		}
		resp.Body = bytes.Clone(body)
	}
	i.response = resp
	return true, nil
}

// respondText is respond for a body the handler wrote as a string.
func (i *instance) respondText(status int, flat []string, text string) bool {
	resp := answer(status, flat)
	resp.Body = wellFormed(text)
	i.response = resp
	return true
}

func answer(status int, flat []string) *invoke.Response {
	resp := &invoke.Response{Status: status}
	for k := 0; k+1 < len(flat); k += 2 {
		if resp.Headers == nil {
			resp.Headers = map[string][]string{}
		}
		name := textproto.CanonicalMIMEHeaderKey(flat[k])
		resp.Headers[name] = append(resp.Headers[name], flat[k+1])
	}
	return resp
}

// encode writes s into the window as UTF-8 and says how long it is, or, when
// it does not fit, the negated length the window must grow to.
func (i *instance) encode(s string) int {
	b := wellFormed(s)
	view, err := i.view(len(b))
	if err != nil {
		return -len(b)
	}
	copy(view, b)
	return len(b)
}

// decode reads the first n bytes of the window as UTF-8: the text and how many
// bytes a stream holds back, or null when fatal refuses them.
func (i *instance) decode(n int, fatal, stream bool) (any, error) {
	data, err := i.view(n)
	if err != nil {
		return nil, err
	}
	text, rest, ok := decodeUTF8(data, fatal, stream)
	if !ok {
		return nil, nil
	}
	return []any{text, rest}, nil
}

// fit puts a request body in the window, growing it from JS, where a full
// arena can collect first.
func (i *instance) fit(body []byte) error {
	if len(body) == 0 {
		return nil
	}
	if _, err := i.rt.Call(i.reserve, engine.Value{}, i.rt.ToValue(len(body))); err != nil {
		return err
	}
	view, err := i.view(len(body))
	if err != nil {
		return err
	}
	copy(view, body)
	return nil
}

// facade runs Go that builds or reads JS values. The engine's constructors
// panic rather than fail when the arena is full, and the panic carries the
// engine's error, so this turns it back into one.
func facade(f func() error) (err error) {
	defer func() {
		if p := recover(); p != nil {
			if e, ok := p.(error); ok {
				err = e
				return
			}
			err = fmt.Errorf("js: the engine panicked: %v", p)
		}
	}()
	return f()
}

// wellFormed is a string as the engine holds it, WTF-8, turned into UTF-8
// with each lone surrogate as U+FFFD, which is what TextEncoder makes of one.
// The engine fuses a surrogate pair into its four-byte form, so every
// three-byte surrogate it holds is a lone one.
func wellFormed(s string) []byte {
	if !strings.Contains(s, "\xed") {
		return []byte(s)
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == 0xed && i+2 < len(s) && s[i+1] >= 0xa0 && s[i+1] <= 0xbf {
			out = append(out, "�"...)
			i += 2
			continue
		}
		out = append(out, s[i])
	}
	return out
}

// decodeUTF8 is the WHATWG UTF-8 decoder. Each maximal ill-formed subsequence
// becomes one U+FFFD, or, when fatal, the input is refused (ok false). With
// stream, an incomplete sequence at the end is held back, and rest is how many
// bytes that is.
func decodeUTF8(b []byte, fatal, stream bool) (text string, rest int, ok bool) {
	var out strings.Builder
	out.Grow(len(b))
	var cp rune
	needed, seen, start := 0, 0, 0
	lower, upper := byte(0x80), byte(0xbf)
	for i := 0; i < len(b); i++ {
		c := b[i]
		if needed == 0 {
			start = i
			switch {
			case c <= 0x7f:
				out.WriteByte(c)
			case c >= 0xc2 && c <= 0xdf:
				needed, cp = 1, rune(c&0x1f)
			case c >= 0xe0 && c <= 0xef:
				if c == 0xe0 {
					lower = 0xa0
				}
				if c == 0xed {
					upper = 0x9f
				}
				needed, cp = 2, rune(c&0x0f)
			case c >= 0xf0 && c <= 0xf4:
				if c == 0xf0 {
					lower = 0x90
				}
				if c == 0xf4 {
					upper = 0x8f
				}
				needed, cp = 3, rune(c&0x07)
			default:
				if fatal {
					return "", 0, false
				}
				out.WriteRune(utf8.RuneError)
			}
			continue
		}
		if c < lower || c > upper {
			if fatal {
				return "", 0, false
			}
			out.WriteRune(utf8.RuneError)
			cp, needed, seen, lower, upper = 0, 0, 0, 0x80, 0xbf
			i-- // the byte starts the next sequence
			continue
		}
		lower, upper = 0x80, 0xbf
		cp = cp<<6 | rune(c&0x3f)
		if seen++; seen == needed {
			out.WriteRune(cp)
			cp, needed, seen = 0, 0, 0
		}
	}
	if needed != 0 {
		if stream {
			return out.String(), len(b) - start, true
		}
		if fatal {
			return "", 0, false
		}
		out.WriteRune(utf8.RuneError)
	}
	return out.String(), 0, true
}
