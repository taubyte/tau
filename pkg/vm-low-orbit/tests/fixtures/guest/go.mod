// Guest wasm test fixtures — compiled with tinygo to reactor modules that
// import the "taubyte/sdk" host functions. Separate module (wasi target); the
// parent tau module ignores it. Rebuild with `make vm-fixtures`.
module github.com/taubyte/tau-vm-test-guest

go 1.22.1

require (
	github.com/ipfs/go-cid v0.0.7
	github.com/taubyte/go-sdk v0.3.9
	golang.org/x/crypto v0.1.0
)

require (
	github.com/btcsuite/btcd/btcec/v2 v2.2.0 // indirect
	github.com/decred/dcrd/dcrec/secp256k1/v4 v4.0.1 // indirect
	github.com/ethereum/go-ethereum v1.10.26 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/klauspost/cpuid/v2 v2.0.12 // indirect
	github.com/mailru/easyjson v0.9.2 // indirect
	github.com/minio/blake2b-simd v0.0.0-20160723061019-3f5f724cb5b1 // indirect
	github.com/minio/sha256-simd v1.0.0 // indirect
	github.com/mr-tron/base58 v1.2.0 // indirect
	github.com/multiformats/go-base32 v0.0.3 // indirect
	github.com/multiformats/go-base36 v0.1.0 // indirect
	github.com/multiformats/go-multibase v0.0.3 // indirect
	github.com/multiformats/go-multihash v0.0.15 // indirect
	github.com/multiformats/go-varint v0.0.6 // indirect
	github.com/taubyte/go-sdk-symbols v0.2.7 // indirect
	golang.org/x/exp v0.0.0-20241204233417-43b7b7cde48d // indirect
	golang.org/x/sys v0.1.0 // indirect
)
