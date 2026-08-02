# monetarium-stratum — node requirements & simnet testing

## Node requirements

For the pool to receive work and submit blocks the monetarium-node RPC server
must be configured as follows:

1. **RPC enabled** with `rpcuser`/`rpcpass` set and `rpclisten` on the address
   the pool connects to (`noderpc`).
2. **`miningaddr` configured**: getwork requires a payout address. Without it
   the node refuses to produce work templates.
3. **`generate` disabled**: `getgenerate` must be false, otherwise the node
   itself mines and does not serve getwork clients.
4. **Synced and connected**: the node must be at the current best block with
   peers, otherwise it will not generate templates.
5. **TLS**: the pool connects over a websocket. Provide the node's `rpc.cert`
   via `rpccert`, or disable TLS with an empty `rpccert` (not recommended).

### Verify the node side

```sh
monetariumctl --configfile=~/.monetarium/monetarium-node.conf getgenerate
monetariumctl --configfile=~/.monetarium/monetarium-node.conf getblockchaininfo
```

`getgenerate` must be `false`.

## Simnet end-to-end test

The following exercises the full path (node → pool → miner → node) on a
simnet node.

### 1. Start a simnet node

```sh
monetarium-node --simnet --listen=127.0.0.1:19508 \
  --rpcuser=user --rpcpass=pass --rpclisten=127.0.0.1:19509 \
  --miningaddr=...simnetaddr... --generate=false
```

### 2. Start the pool

```sh
./monetarium-stratum --noderpc=127.0.0.1:19509 --rpcuser=user --rpcpass=pass \
  --rpccert=~/.monetarium/simnet/rpc.cert --listen=127.0.0.1:5550
```

### 3. Connect a miner

Use gominer or any stratum client:

```sh
gominer -P stratum+tcp://127.0.0.1:5550 --user simnet-worker --password x
```

### 4. Drive a block

With `miningaddr` set and the pool connected, mine a block on the simnet node
itself (e.g. `generate 1` via the node's RPC) and confirm:

- The pool logs the block-connected notification and serves refreshed work.
- A valid share submission from the miner is accepted.
- A solved block (below the network target) is submitted to the node and
  accepted.

Because a real block solution is required to exercise the throttle path, the
submit path is validated in unit tests with a stubbed evaluator and the
deterministic decisions (`Rejected`, `AcceptedShare`, `SubmitBlock`).
