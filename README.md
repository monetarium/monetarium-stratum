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

The module builds against the published `monetarium-node` releases on the Go
module proxy (see `go.mod`), so no local checkout is required.

The bundled miners build as follows:

```sh
go build -o cpuminer ./cmd/cpuminer   # CPU stratum miner
make -C cmd/gpuminer                  # GPU stratum miner (also builds the OpenCL host)
```

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

## Test with the simulated ASIC (CPU miner)

`cmd/cpuminer` is a CPU stratum miner that stands in for an ASIC device.  It
connects over plain stratum, reconstructs the block header from each
`mining.notify` (the same layout gominer uses), searches the nonce space with
real blake3 hashing, and submits shares/blocks back to the pool.  Use it to
exercise the full path `node -> pool -> miner -> pool -> node` without any
ASIC hardware.

```sh
# from the repo root
go build -o monetarium-stratum .
go build -o cpuminer ./cmd/cpuminer

./monetarium-stratum --sharedifficulty=1 --rpcuser=user --rpcpass=pass
./cpuminer --pool 127.0.0.1:5550 --user worker1 --password x \
  --net mainnet --threads 4
```

The miner reports hashrate, accepted/rejected shares and found blocks every few
seconds.

Flags: `--pool` (default `127.0.0.1:5550`), `--user`, `--password`,
`--net` (`mainnet`, `testnet3`, `simnet`, `regnet`; default `mainnet`),
`--threads` (parallel hashing, default 1), `--debug`.

## GPU mining

`cmd/gpuminer` is a stratum-only GPU miner.  It drives a small OpenCL host
subprocess that runs the BLAKE3 nonce search on the GPU, while the Go process
handles the stratum connection, job reconstruction and share submission.  There
is no getwork/RPC path — it talks to the pool exactly like `cmd/cpuminer`.

The GPU searches the full 2^32 nonce space for one header; when a sweep
completes without a solution (or after a share is found) the miner rolls
`extraNonce2` and re-sends the same job with the new value, giving a fresh
disjoint nonce space per sweep.

Requirements: a working OpenCL implementation (the host lists the platforms and
devices it finds on startup and picks the first physical GPU), plus a C++
compiler to build the host.

```sh
# from the repo root
make -C cmd/gpuminer        # builds the OpenCL host and the gpuminer binary

./cmd/gpuminer/gpuminer --pool 127.0.0.1:5550 --user worker1 --password x \
  --net mainnet
```

The miner reports hashrate, accepted/rejected shares and found blocks every few
seconds.

Flags: `--pool` (default `127.0.0.1:5550`), `--user`, `--password`,
`--net` (`mainnet`, `testnet3`, `simnet`, `regnet`; default `mainnet`),
`--host` (path to the OpenCL host binary, default `./host`),
`--kernels` (OpenCL kernel directory, default `./cl`), `--debug`.

### Share difficulty on a young network

The pool's share target is `PowLimit / sharedifficulty`.  On a network whose
block difficulty is still near `PowLimit` (a young chain where CPU miners can
solve blocks), keep `--sharedifficulty=1` so the share target equals the block
target; the default of 100 would otherwise demand 100x more work for a share
than for a block.  At `--sharedifficulty=1` every accepted share is a block
solution the pool submits to the node.

### Node preconditions

The node serving the pool must have `generate` disabled (getwork refuses to
serve work while the node mines itself), be synced to the current tip and have
at least one peer.  Check with `monetarium-ctl`:

```sh
monetarium-ctl --configfile=~/.monetarium/monetarium.conf getgenerate
monetarium-ctl --configfile=~/.monetarium/monetarium.conf getblockchaininfo
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

Install the binaries and the units, then let systemd manage the whole stack.

### Binaries

Install each binary to `/usr/local/bin`:

```sh
go build -o /usr/local/bin/monetarium-stratum .
go build -o /usr/local/bin/cpuminer ./cmd/cpuminer
make -C cmd/gpuminer                     # builds gpuminer and the OpenCL host
install -m 0755 cmd/gpuminer/gpuminer /usr/local/bin/gpuminer
install -m 0755 cmd/gpuminer/host /usr/local/bin/host
install -d /usr/local/lib/monetarium-gpuminer
cp -r cmd/gpuminer/cl /usr/local/lib/monetarium-gpuminer/
```

The GPU miner needs the OpenCL host binary and the `cl/` kernel directory at
the absolute paths it is started with; the units below expect them at
`/usr/local/bin/host` and `/usr/local/lib/monetarium-gpuminer/cl`.

### systemd units

Copy the units, create the system user, and enable them:

```sh
install -m 0644 systemd/monetarium-stratum.service  /etc/systemd/system/
install -m 0644 systemd/monetarium-cpuminer.service /etc/systemd/system/
install -m 0644 systemd/monetarium-gpuminer.service /etc/systemd/system/

# system user under which the services run. The GPU miner caches state in the
# home directory, so it needs a real, writable home rather than /nonexistent,
# and membership in the render group to access the GPU's /dev entries.
useradd --system --create-home --home-dir /var/local/monetarium \
	--shell /usr/sbin/nologin -G render monetarium

install -d -o monetarium -g monetarium /etc/monetarium-stratum
install -m 0644 sample-monetarium-stratum.conf /etc/monetarium-stratum/monetarium-stratum.conf

systemctl daemon-reload
systemctl enable --now monetarium-stratum.service
# only if you want a miner on this host
systemctl enable --now monetarium-cpuminer.service     # or monetarium-gpuminer.service
```

The pool unit starts after `monetarium-node.service` and requires it; the miner
units start after (and restart with) the pool. The stratum pool also reads its
configuration from `--configfile=/etc/monetarium-stratum/monetarium-stratum.conf`
(copy `sample-monetarium-stratum.conf` and edit `rpcuser`/`rpcpass`/`rpccert`).

The miners have no config file; set their flags directly in the unit's
`ExecStart`:

- `monetarium-cpuminer.service` — `--user`, `--password`, `--threads`.
- `monetarium-gpuminer.service` — `--user`, `--password`, and (only if you
  changed the deploy paths) `--host` / `--kernels`.

After editing a unit, run `systemctl daemon-reload` and `systemctl restart
monetarium-cpuminer.service` (or the GPU unit).

Check status with `systemctl status monetarium-{stratum,cpuminer,gpuminer}.service`.

## Development

Run the test suite:

```sh
go test ./...
```

Tests cover header field placement, the notify format round trip (gominer's
exact reconstruction), share/block evaluation, the work manager, the stratum
protocol, the full submit path, the block throttle matrix, and the GPU miner's
header reconstruction, little-endian share target and extraNonce2 rollover.

## License

ISC (see LICENSE).
