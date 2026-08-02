# monetarium-stratum

A solo stratum mining pool server for Monetarium. It connects to a
monetarium-node instance, streams fresh work to connected stratum miners (ASICs
and GPUs), validates their shares, and submits solved blocks back to the node
via the getwork RPC.

It implements the Decred-style stratum dialect used by BLAKE3-era miners
(gominer and compatible clients).

## Features

- **Solo mining pool** over stratum: subscribe/authorize/notify/submit.
- **Live work notifications** from the node over websockets (`NotifyWork`),
  with an initial `getwork` fetch so miners get work immediately.
- **Share and block validation** using the node's own blake3 proof of work
  (`PowHashV2`), with per-share difficulty checking.
- **Self-limiting block submission** (`--blocksubmitdivisor=N`): only 1 in
  every N solved blocks is submitted to the network, so a pool whose hardware
  would otherwise find more than 51% of the network's blocks can keep its
  effective submitted hashrate proportional to the rest of the network. The
  discarded blocks are logged and counted, and miners see them as accepted
  shares.

## Requirements

- A running, synced `monetarium-node` with RPC enabled and a `miningaddr`
  configured (so getwork templates can be generated). The node must not be in
  `generate` mode.
- The node RPC certificate (`rpc.cert`) accessible to this pool.
- Go 1.23 or newer to build.

## Build

```sh
go build -o monetarium-stratum .
```

The module uses `replace` directives to build against the local
`../monetarium-node` checkout, which is the authoritative source for the
`rpcclient`, `wire` and `blockchain/standalone` packages. Adjust the paths in
`go.mod` if your checkout lives elsewhere.

## Configure

Copy `sample-monetarium-stratum.conf` to
`~/.monetarium-stratum/monetarium-stratum.conf` and edit it. The essential
settings:

```
noderpc=127.0.0.1:9509
rpcuser=your-node-rpc-user
rpcpass=your-node-rpc-pass
rpccert=/home/user/.monetarium/rpc.cert
```

See the sample file for the full option list, including the block submission
throttle.

## Run

```sh
./monetarium-stratum
```

Point your miner at the pool:

```
stratum+tcp://HOST:5550
user=anything       # accepted unless poolpassword is set
password=x          # must match poolpassword when set
```

Example for `gominer`:

```sh
gominer -P stratum+tcp://127.0.0.1:5550 --user worker1 --password x
```

## Self-limiting block submission

If your ASIC or GPU farm finds blocks faster than the rest of the network
combined, you risk centralizing block production. Set the divisor so that your
effective submitted hashrate is a sane fraction:

| `blocksubmitdivisor` | effective submitted hashrate |
|----------------------|------------------------------|
| 1 (default)          | 100% (no throttling)         |
| 2                    | 50%                          |
| 10                   | 10%                          |

Solved blocks that the throttle discards are logged and counted; they are
simply never submitted to the node, so they never reach the network. The
counters are shown in the periodic stats log line.

## Deployment

Install the binary to `/usr/local/bin/monetarium-stratum` and use the provided
systemd unit (`systemd/monetarium-stratum.service`), which starts the pool
after `monetarium-node.service`.

## Development

Run the test suite:

```sh
go test ./...
```

Tests cover header field placement, the notify format round trip (gominer's
exact reconstruction), share/block evaluation, the work manager, the stratum
protocol, the full submit path, and the block throttle matrix.

## License

ISC (see LICENSE).
