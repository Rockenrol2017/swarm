# S.W.A.R.M. Architecture

## Full Stack Diagram

```
┌─────────────────────────────────────────────┐
│  LAN devices (phone, laptop, TV, console)   │
│  No configuration needed on devices         │
└──────────────────┬──────────────────────────┘
                   │ default gateway
┌──────────────────▼──────────────────────────┐
│  S.W.A.R.M. client node (home server)       │
│                                             │
│  ┌─────────────────────────────────────┐   │
│  │ TPROXY (iptables)                   │   │
│  │ Intercepts all outbound traffic     │   │
│  └──────────────┬──────────────────────┘   │
│  ┌──────────────▼──────────────────────┐   │
│  │ SOCKS5 proxy (:1090)                │   │
│  │ Counts bytes up/down                │   │
│  └──────────────┬──────────────────────┘   │
│  ┌──────────────▼──────────────────────┐   │
│  │ QUIC tunnel                         │   │
│  │ ChaCha20-Poly1305 encryption        │   │
│  │ X25519 key exchange                 │   │
│  └──────────────┬──────────────────────┘   │
└─────────────────┼───────────────────────────┘
                  │ encrypted QUIC
┌─────────────────▼───────────────────────────┐
│  Bootstrap node (VPS)                        │
│  Accepts connections, proxies to internet    │
└──────────────────────────────────────────────┘
```

## Node Modes

**Bootstrap** — always-on entry point
- Listens for incoming QUIC connections
- Proxies requests to internet
- Runs on VPS with public IP

**Relay** — 2-hop forwarding node
- Accepts client connections
- Forwards to bootstrap upstream
- Prevents direct client-to-bootstrap path, adds one more hop

**Client** — end-user node
- Connects to bootstrap or relay
- Exposes SOCKS5 on localhost
- Integrates with TPROXY for transparent proxying of all LAN traffic

## Crypto Handshake

```
Client                          Server
  |                               |
  |── Hello (X25519 public key) ──>|
  |<─ Hello (X25519 public key) ──|
  |                               |
  | [Both derive shared secret via X25519 DH]
  | [HKDF-SHA256 → session key]   |
  |                               |
  |══ ChaCha20-Poly1305 tunnel ══|
```

Each session uses an ephemeral X25519 key pair.
Node identity is verified with Ed25519 signatures.

## Relay Forwarding

```
Client ──QUIC──> Relay ──QUIC──> Bootstrap ──TCP──> Internet
```

- Client opens a QUIC stream to relay with target address
- Relay calls `selectUpstreamPeer()` — only outgoing (upstream) peers
- Relay opens a QUIC stream to bootstrap with the same target
- Bidirectional data bridging via `bridgeStreamTCP()`
- `isOutgoing bool` in Peer struct prevents routing loops

## Traffic Monitoring

- `bytes_up` / `bytes_down` — session counters (`atomic.Int64`, reset on restart)
- `bytes_today` / `bytes_month` — persistent (`/var/lib/swarm/traffic.json`, saved every 60s)
- Auto-resets on day/month boundary check
- `limit_percent` — percentage of configured ISP monthly limit
- `latency_ms` — RTT to bootstrap node (HTTP GET to `/health`, every 30s)
  - `0` = not yet measured
  - `-1` = unreachable
  - `>0` = RTT in milliseconds

## API Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/status` | GET | Full node status JSON |
| `/api/peers` | GET | List of connected peers |
| `/health` | GET | Liveness check (200 OK) |

## Configuration (Config struct)

| Field | Type | Description |
|-------|------|-------------|
| `mode` | string | `bootstrap` / `relay` / `client` |
| `listen_addr` | string | QUIC listen address (e.g. `:7437`) |
| `bootstrap_addr` | string | Bootstrap node address (client/relay) |
| `socks5_addr` | string | SOCKS5 proxy address (e.g. `:1090`) |
| `tproxy_addr` | string | TPROXY listen address |
| `status_addr` | string | HTTP API address (e.g. `:19090`) |
| `identity_file` | string | Ed25519 key storage path |
| `traffic_file` | string | Persistent traffic counter path |
| `skyedge_limit_gb` | float64 | Monthly traffic limit in GB (for monitoring) |
| `max_peers` | int | Maximum concurrent peer connections |

## Directory Structure

```
pkg/swarmproto/      — wire protocol, crypto primitives
  handshake.go       — X25519 DH + Ed25519 handshake
  cipher.go          — ChaCha20-Poly1305 session encryption
  packet.go          — framing and message types

pkg/swarmnode/       — node runtime
  node.go            — lifecycle, peer management, goroutines
  peer.go            — QUIC connections, relay forwarding
  socks5.go          — SOCKS5 proxy with byte counting
  tproxy.go          — transparent proxy (SO_TRANSPARENT)
  traffic.go         — persistent daily/monthly counters
  latency.go         — RTT probe goroutine
  status.go          — HTTP API handlers
  peers_exchange.go  — peer discovery and announcement

cmd/swarm-node/      — main binary entrypoint
cmd/swarm-monitor/   — web dashboard binary
swarm-monitor/       — dashboard HTML/JS frontend
install/             — deployment and setup scripts
```
