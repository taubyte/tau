#[allow(warnings)]
mod bindings;

use bindings::{Direction, Guest, Shaped, Traffic, Verdict};

// A REAL PROXY TRANSFORMER, and the only one in the tree.
//
// It does the two things nothing else in this tree can show:
//
//   IT BUFFERS. Inbound bytes are held until a newline arrives, and what it
//   passes on is the whole line, uppercased. A pass-through fixture would pass
//   whether or not the host carried the state back at all — which is the one
//   thing this world's design rests on.
//
//   IT CLOSES. A line reading "stop" ends the connection, which is a decision
//   and not a failure.
//
// Outbound bytes are prefixed once per chunk, so a test can tell the two
// directions apart and prove the host called it for both.
struct Component;

impl Guest for Component {
    fn transform(t: Traffic) -> Verdict {
        match t.way {
            Direction::Outbound => Verdict::Pass(Shaped {
                out: if t.chunk.is_empty() {
                    Vec::new()
                } else {
                    let mut out = b"<".to_vec();
                    out.extend_from_slice(&t.chunk);
                    out
                },
                // Nothing to remember in this direction, and saying so is how
                // a transformer forgets.
                state: Vec::new(),
            }),

            Direction::Inbound => {
                let mut held = t.state.clone();
                held.extend_from_slice(&t.chunk);

                let mut out = Vec::new();
                while let Some(at) = held.iter().position(|b| *b == b'\n') {
                    let line: Vec<u8> = held.drain(..=at).collect();
                    if line.starts_with(b"stop") {
                        return Verdict::Close("it was asked to stop".to_string());
                    }
                    out.extend(line.iter().map(|b| b.to_ascii_uppercase()));
                }

                // THE LAST CHUNK IS THE LAST CHANCE. Whatever is still held has
                // no newline coming, so it goes out as it is rather than being
                // swallowed.
                if t.last && !held.is_empty() {
                    out.extend(held.iter().map(|b| b.to_ascii_uppercase()));
                    held.clear();
                }

                Verdict::Pass(Shaped { out, state: held })
            }
        }
    }
}

bindings::export!(Component with_types_in bindings);
