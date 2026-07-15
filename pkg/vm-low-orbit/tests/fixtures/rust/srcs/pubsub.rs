extern crate taubyte_sdk;

use taubyte_sdk::{
    event::{Event, EventType},
    http::Client,
    pubsub::{self, Channel},
};

#[no_mangle]
pub fn pubsubtest(event: Event) {
    match event.event_type() {
        EventType::EventTypeUndefined => {
            panic!("Event type is undefined")
        }
        EventType::EventTypeHttp => {
            let http = event.http().unwrap();
            let query = http.queries().get("name").unwrap();

            let err: Result<(), Box<dyn std::error::Error>>;
            match query.as_str() {
                "pubstuff" => {
                    err = do_attach_pubsub();
                }
                "actuallypublish" => {
                    err = do_publish();
                }
                _ => {
                    panic!("unknown query: {}", query);
                }
            }

            if err.is_err() {
                let err =
                    http.write(format!("{} failed with: {}", query, err.err().unwrap()).as_bytes());
                if err.is_err() {
                    panic!("Err writing: {}", err.err().unwrap())
                }
            }
        }
        EventType::EventTypePubsub => {
            let pubsub = event.pubsub().unwrap();
            let err = do_pubsub(pubsub);
            if err.is_err() {
                panic!("Err with do_pubsub: {}", err.err().unwrap())
            }
        }
        EventType::EventTypeP2P => {
            panic!("event type is EventTypeP2P");
        }
    }
}

fn do_pubsub(p: pubsub::Event) -> Result<(), Box<dyn std::error::Error>> {
    let channel = p.channel()?;
    assert_eq!(channel.name(), "someChannel");

    let data = p.data()?;
    if String::from_utf8(data)? == "Hello, world" {
        Client::post("http://localhost:9090/pubsub")?;
    }

    Ok(())
}

fn get_channel() -> Result<Channel, Box<dyn std::error::Error>> {
    Channel::new("someChannel".to_string())
}

fn do_publish() -> Result<(), Box<dyn std::error::Error>> {
    get_channel()?.publish("Hello, world".as_bytes())
}

fn do_attach_pubsub() -> Result<(), Box<dyn std::error::Error>> {
    get_channel()?.subscribe()
}
