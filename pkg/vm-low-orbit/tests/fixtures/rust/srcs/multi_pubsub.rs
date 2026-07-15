use taubyte_sdk::{event::Event, http::Client, pubsub};

#[no_mangle]
pub fn multi_pubsubtest(event: Event) {
    let pubsub = event.pubsub().unwrap();
    let err = run_multi_test(pubsub);
    if err.is_err() {
        panic!("multi_pubsubtest failed: {:?}", err);
    }
}

fn run_multi_test(p: pubsub::Event) -> Result<(), Box<dyn std::error::Error>> {
    let channel = p.channel()?;
    assert_eq!(channel.name(), "someChannel");

    let data = p.data()?;
    if data == "Hello, world".as_bytes() {
        Client::post("http://localhost:9090/multi_pubsub")?;
    }

    Ok(())
}
