extern crate taubyte_sdk;
use std::io::Read;

use taubyte_sdk::{event::Event, http, storage::Storage};

#[no_mangle]
pub fn storagetest(event: Event) {
    let http = event.http().unwrap();

    let err = do_test(http);
    if err.is_err() {
        let err = http.write(format!("storagetest failed with: {}", err.err().unwrap()).as_bytes());
        if err.is_err() {
            println!("Err writing: {}", err.err().unwrap())
        }
    }
}

fn do_test(h: http::Event) -> Result<(), Box<dyn std::error::Error>> {
    let file_data_1 = "Hello, world";
    let file_data_2 = "Hello, world!";
    let expected_cid_1 = "bafybeiej6xs3wwj5a6zoknxg3ovsmq45ekogp7673g4g6jhgkhki7dv2aa";

    let storage = Storage::new("someStorage")?;

    let mut video_file = storage.file("video").as_versioned(0);
    let version = video_file.add(file_data_1.as_bytes(), false)?;
    assert_eq!(version, 1);

    let current_version = video_file.latest_version()?;
    assert_eq!(current_version, version);

    let cid = storage.cid("video")?;
    assert_eq!(expected_cid_1, cid.to_string());

    let versions = video_file.versions()?;
    assert_eq!(versions.len(), 1);

    let mut video_file = storage.file("video").as_versioned(1);
    let mut file = video_file.get()?;

    let mut buf = String::new();
    file.read_to_string(&mut buf)?;
    assert_eq!(buf, file_data_1);

    let new_version = video_file.add(file_data_2.as_bytes(), false)?;
    assert_eq!(new_version, 2);

    let video_file = storage.file("video").as_versioned(2);
    let mut file = video_file.get()?;

    let mut buf = String::new();
    file.read_to_string(&mut buf)?;
    assert_eq!(buf, file_data_2);

    let versions = video_file.versions()?;
    assert_eq!(versions.len(), 2);

    let files = storage.list_files()?;
    assert_eq!(files.len(), 2);

    let used = storage.used()?;
    assert_eq!(used, 25);

    let cap = storage.remaining_capacity()?;
    assert_eq!(cap, 4975);

    h.write("Success".as_bytes())?;
    Ok(())
}
