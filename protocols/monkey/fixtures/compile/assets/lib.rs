extern crate taubyte_sdk;

use taubyte_sdk::event::Event;

#[no_mangle]
pub fn do_stuff(event: Event) {
    let http = event.http().unwrap();


    use std::io::Read;
    let mut body = http.body();
    let mut buffer = String::new();
    body.read_to_string(&mut buffer).unwrap();

    buffer.push_str(&"Hello world".to_string());
    let _ = http.write(buffer.as_bytes());
}