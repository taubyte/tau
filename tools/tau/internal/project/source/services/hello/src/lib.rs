// The function `tau init` starts you with. It is built against tau's world in
// wit/ at the root of this project, and answers every request with a greeting
// and the path it was asked for.
wit_bindgen::generate!({
    path: "../../wit",
    world: "handler",
    generate_all,
});

struct Hello;

impl Guest for Hello {
    fn handle(req: Request) -> Response {
        Response {
            status: 200,
            headers: vec![Header {
                name: "content-type".to_string(),
                value: "text/plain".to_string(),
            }],
            body: format!("hello from {}\n", req.path).into_bytes(),
        }
    }
}

export!(Hello);
