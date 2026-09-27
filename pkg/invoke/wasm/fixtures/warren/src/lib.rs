#[allow(warnings)]
mod bindings;

use bindings::{Answer, Guest, Question};

// A REAL DNS RESOLVER, and the only one in the tree.
//
// It drives all three arms of `answer`, which is the whole shape of this world
// and the reason it is not a bare list:
//
//   "geo"    -> records, in zone-file notation relative to the apex
//   "quiet"  -> empty,  the name is here and holds nothing of that type
//   anything -> absent, no such name (NXDOMAIN, and cacheable as one)
struct Component;

impl Guest for Component {
    fn resolve(q: Question) -> Answer {
        let label = q.name.trim_end_matches('.').split('.').next().unwrap_or("");
        match label {
            // ANSWERED PER TYPE, and not one line with the type spliced into it.
            // That is what a resolver actually has to do -- an A holds an IPv4
            // address and an AAAA holds an IPv6 one, so a single template
            // produces a line that cannot be parsed as the type it names. The
            // host would drop it and the name would resolve to nothing.
            "geo" => Answer::Records(vec![match q.type_.as_str() {
                "AAAA" => "geo 60 IN AAAA 2001:db8::9".to_string(),
                "TXT" => "geo 60 IN TXT \"at 203.0.113.9\"".to_string(),
                "MX" => "geo 60 IN MX 10 mail.example.com.".to_string(),
                other => format!("geo 60 IN {} 203.0.113.9", other),
            }]),
            "quiet" => Answer::Empty,
            _ => Answer::Absent,
        }
    }
}

bindings::export!(Component with_types_in bindings);
