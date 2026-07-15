extern crate taubyte_sdk;
use std::io::Read;

use taubyte_sdk::{event::Event, i2mv::memview::ReadSeekCloser};

#[link(wasm_import_module = "fs//tmp/492934390/artifact.wasm")]
extern "C" {
    pub fn mv_1() -> u32;
}

#[no_mangle]
pub fn mv_2(event: Event) {
    let h = event.http().unwrap();

    let id = unsafe { mv_1() };

    let mut buffer = String::new();
    let mut mv = ReadSeekCloser::open(id).unwrap();

    let n = mv.read_to_string(&mut buffer).unwrap();
    if n != 11 {
        h.write(format!("expected length `11` got `{}`", n).as_bytes())
            .unwrap();
    } else {
        if buffer != "hello world" {
            h.write(format!("expected `hello world  got `{}`", buffer).as_bytes())
                .unwrap();
        } else {
            h.write(buffer.as_bytes());
        }
    }
}
