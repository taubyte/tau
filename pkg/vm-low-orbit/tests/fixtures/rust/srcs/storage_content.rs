extern crate taubyte_sdk;
use std::io::{Read, Seek, SeekFrom, Write};

use taubyte_sdk::{
    event::Event,
    http,
    storage::{Content, Storage},
};

#[no_mangle]
pub fn storageContentTest(event: Event) {
    let http = event.http().unwrap();

    let err = do_test(http);
    if err.is_err() {
        let err = http
            .write(format!("storageContentTest failed with: {}", err.err().unwrap()).as_bytes());
        if err.is_err() {
            println!("Err writing: {}", err.err().unwrap())
        }
    }
}

fn do_test(h: http::Event) -> Result<(), Box<dyn std::error::Error>> {
    let data = "Hello World";
    let data2 = " Hello World AGAIN";
    let expected_cid = "bafybeidqnk6czrgcaydbxw54tf2qpjmd5pcefpeoudytygrbukfoaawo4i";

    let mut content = Content::new()?;
    content.write_all(data.as_bytes())?;

    // Should fail since its at the end
    let mut buf = String::new();
    content.read_to_string(&mut buf)?;
    assert_eq!(buf, "");

    content.seek(SeekFrom::Start(0))?;

    let mut buf = String::new();
    content.read_to_string(&mut buf)?;
    assert_eq!(buf, data);

    content.write_all(data2.as_bytes())?;

    content.seek(SeekFrom::Start(0))?;

    let mut buf = String::new();
    content.read_to_string(&mut buf)?;
    assert_eq!(buf, (data.to_string() + data2));

    let cid = content.push()?;
    assert_eq!(cid.to_string(), expected_cid);
    content.close()?;

    let mut get_content = Content::open(cid)?;
    get_content.seek(SeekFrom::Start(0))?;

    let mut buf = String::new();
    get_content.read_to_string(&mut buf)?;
    assert_eq!(buf, (data.to_string() + data2));

    h.write("Success".as_bytes())?;
    Ok(())
}
