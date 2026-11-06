# Built from the repository root (docker build -f operator/Dockerfile .):
# kates-crds reads schemas/ with include_str!.
FROM rust:1.99.1-bookworm@sha256:7085845219c18e7c5b1f34ed9ce89b84f822f2e3d54a02774cedc996ac0f304f AS builder
RUN apt-get update && apt-get install -y --no-install-recommends musl-tools && rm -rf /var/lib/apt/lists/*

WORKDIR /build
COPY schemas/ schemas/
COPY operator/ operator/

WORKDIR /build/operator
RUN target="$(uname -m)-unknown-linux-musl" \
    && rustup target add "$target" \
    && cargo build --release --locked --target "$target" -p kates-operator --features telemetry \
    && cp "target/$target/release/kates-operator" /kates-operator

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=builder /kates-operator /kates-operator
ENTRYPOINT ["/kates-operator"]
