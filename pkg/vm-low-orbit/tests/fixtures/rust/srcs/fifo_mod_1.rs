extern crate taubyte_sdk;

use std::io::Write;

use taubyte_sdk::i2mv::fifo::WriteCloser;

#[no_mangle]
pub fn fifo_1() -> u32 {
    let mut ff = WriteCloser::new(true);
    ff.write("hello world".as_bytes()).unwrap();

    ff.id
}
