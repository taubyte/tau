extern crate taubyte_sdk;
use std::io::Read;

use taubyte_sdk::{event::Event, i2mv::fifo::ReadCloser};

#[link(wasm_import_module = "fs//tmp/3748203861/artifact.wasm")]
extern "C" {
    pub fn fifo_1() -> u32;
}

#[no_mangle]
pub fn fifo_2(event: Event) {
    let h = event.http().unwrap();

    let id = unsafe { fifo_1() };

    let mut buffer = String::new();
    let mut ff = ReadCloser::open(id).unwrap();

    let n = ff.read_to_string(&mut buffer).unwrap();
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
