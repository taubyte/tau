# The toolchain the fixtures are built with, as an image: what the corpus
# projects under node/dream/projects name as their build image, so `tau build`
# and a cloud's build make the committed components from source. Tagged
# tau-fixtures/rust-component:0.21.1 by whatever builds it (the corpus harness
# does, once per machine).
FROM rust:1.90-slim-bookworm
RUN rustup target add wasm32-wasip1 wasm32-wasip2 \
 && cargo install cargo-component --version 0.21.1 --locked \
 && rm -rf /usr/local/cargo/registry/src /usr/local/cargo/registry/cache
