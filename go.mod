module github.com/monetarium/monetarium-stratum

go 1.23.0

toolchain go1.23.4

require (
	github.com/decred/slog v1.2.0
	github.com/jessevdk/go-flags v1.6.1
	github.com/monetarium/monetarium-node/blockchain/standalone v1.3.9
	github.com/monetarium/monetarium-node/chaincfg v1.3.9
	github.com/monetarium/monetarium-node/chaincfg/chainhash v1.3.9
	github.com/monetarium/monetarium-node/rpcclient v1.3.9
	github.com/monetarium/monetarium-node/wire v1.3.9
)

require (
	github.com/agl/ed25519 v0.0.0-20170116200512-5312a6153412 // indirect
	github.com/dchest/siphash v1.2.3 // indirect
	github.com/decred/base58 v1.0.5 // indirect
	github.com/decred/dcrd/crypto/blake256 v1.0.1 // indirect
	github.com/decred/go-socks v1.1.0 // indirect
	github.com/gorilla/websocket v1.5.1 // indirect
	github.com/klauspost/cpuid/v2 v2.0.9 // indirect
	github.com/monetarium/monetarium-node/blockchain/stake v1.3.9 // indirect
	github.com/monetarium/monetarium-node/cointype v1.3.9 // indirect
	github.com/monetarium/monetarium-node/crypto/blake256 v1.3.9 // indirect
	github.com/monetarium/monetarium-node/crypto/rand v1.3.9 // indirect
	github.com/monetarium/monetarium-node/crypto/ripemd160 v1.3.9 // indirect
	github.com/monetarium/monetarium-node/database v1.3.9 // indirect
	github.com/monetarium/monetarium-node/dcrec v1.3.9 // indirect
	github.com/monetarium/monetarium-node/dcrec/edwards v1.3.9 // indirect
	github.com/monetarium/monetarium-node/dcrec/secp256k1 v1.3.9 // indirect
	github.com/monetarium/monetarium-node/dcrjson v1.3.9 // indirect
	github.com/monetarium/monetarium-node/dcrutil v1.3.9 // indirect
	github.com/monetarium/monetarium-node/gcs v1.3.9 // indirect
	github.com/monetarium/monetarium-node/rpc/jsonrpc/types v1.3.9 // indirect
	github.com/monetarium/monetarium-node/txscript v1.3.9 // indirect
	golang.org/x/crypto v0.33.0 // indirect
	golang.org/x/net v0.25.0 // indirect
	golang.org/x/sys v0.30.0 // indirect
	lukechampine.com/blake3 v1.3.0 // indirect
)

replace github.com/monetarium/monetarium-node/addrmgr => ../monetarium-node/addrmgr

replace github.com/monetarium/monetarium-node/bech32 => ../monetarium-node/bech32

replace github.com/monetarium/monetarium-node/blockchain => ../monetarium-node/blockchain

replace github.com/monetarium/monetarium-node/blockchain/stake => ../monetarium-node/blockchain/stake

replace github.com/monetarium/monetarium-node/blockchain/standalone => ../monetarium-node/blockchain/standalone

replace github.com/monetarium/monetarium-node/certgen => ../monetarium-node/certgen

replace github.com/monetarium/monetarium-node/chaincfg => ../monetarium-node/chaincfg

replace github.com/monetarium/monetarium-node/chaincfg/chainhash => ../monetarium-node/chaincfg/chainhash

replace github.com/monetarium/monetarium-node/cointype => ../monetarium-node/cointype

replace github.com/monetarium/monetarium-node/connmgr => ../monetarium-node/connmgr

replace github.com/monetarium/monetarium-node/container/apbf => ../monetarium-node/container/apbf

replace github.com/monetarium/monetarium-node/container/lru => ../monetarium-node/container/lru

replace github.com/monetarium/monetarium-node/crypto/blake256 => ../monetarium-node/crypto/blake256

replace github.com/monetarium/monetarium-node/crypto/rand => ../monetarium-node/crypto/rand

replace github.com/monetarium/monetarium-node/crypto/ripemd160 => ../monetarium-node/crypto/ripemd160

replace github.com/monetarium/monetarium-node/database => ../monetarium-node/database

replace github.com/monetarium/monetarium-node/dcrec => ../monetarium-node/dcrec

replace github.com/monetarium/monetarium-node/dcrec/edwards => ../monetarium-node/dcrec/edwards

replace github.com/monetarium/monetarium-node/dcrec/secp256k1 => ../monetarium-node/dcrec/secp256k1

replace github.com/monetarium/monetarium-node/dcrjson => ../monetarium-node/dcrjson

replace github.com/monetarium/monetarium-node/dcrutil => ../monetarium-node/dcrutil

replace github.com/monetarium/monetarium-node/gcs => ../monetarium-node/gcs

replace github.com/monetarium/monetarium-node/hdkeychain => ../monetarium-node/hdkeychain

replace github.com/monetarium/monetarium-node/math/uint256 => ../monetarium-node/math/uint256

replace github.com/monetarium/monetarium-node/mixing => ../monetarium-node/mixing

replace github.com/monetarium/monetarium-node/peer => ../monetarium-node/peer

replace github.com/monetarium/monetarium-node/rpc/jsonrpc/types => ../monetarium-node/rpc/jsonrpc/types

replace github.com/monetarium/monetarium-node/rpcclient => ../monetarium-node/rpcclient

replace github.com/monetarium/monetarium-node/txscript => ../monetarium-node/txscript

replace github.com/monetarium/monetarium-node/wire => ../monetarium-node/wire
