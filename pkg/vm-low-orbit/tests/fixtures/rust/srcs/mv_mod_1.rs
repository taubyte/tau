extern crate taubyte_sdk;

use taubyte_sdk::i2mv::memview::Closer;

#[no_mangle]
pub fn mv_1() -> u32 {
    let ff = Closer::new("hello world".as_bytes(), true).unwrap();

    ff.id
}
