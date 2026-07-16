extern crate taubyte_sdk;

use taubyte_sdk::{database::Database, event::Event, http};

#[no_mangle]
pub fn databasetest(event: Event) {
    let http = event.http().unwrap();

    let err = do_test(http);
    if err.is_err() {
        let err =
            http.write(format!("databasetest failed with: {}", err.err().unwrap()).as_bytes());
        if err.is_err() {
            println!("Err writing: {}", err.err().unwrap())
        }
    }
}

fn do_test(h: http::Event) -> Result<(), Box<dyn std::error::Error>> {
    let expected = "DatabaseTest";

    let database = Database::new("someDatabase")?;

    database.put("test", expected.as_bytes())?;
    database.put("test/1", expected.as_bytes())?;
    database.put("test/1s", expected.as_bytes())?;

    let test = database.get("test")?;
    let n = h.write(&test)?;
    assert_eq!(n, test.len() as u32);

    let test_string = std::str::from_utf8(&test)?;
    assert_eq!(test_string, expected);

    database.delete("test")?;

    let err = database.get("test");
    if err.is_ok() {
        return Err(format!("Expected error for key: `test`").into());
    }

    let keys = database.list("")?;
    if keys.len() != 2 {
        // KEYS: ["/test/1", "/test/1s"]
        return Err(format!("Expected 4 keys, got {}", keys.len()).into());
    }

    let keys = database.list("test")?;
    if keys.len() != 2 {
        // KEYS: ["/test/1", "/test/1s"]
        return Err(format!("Expected 2 keys, got {}", keys.len()).into());
    }

    Ok(())
}
