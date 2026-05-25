# S.W.A.R.M.
### Secure · Worldwide · Anonymous · Routing · Mesh

> A decentralized peer-to-peer network built for privacy and security.
> The more participants — the stronger and more resilient it becomes.

[![License: GPL v3](https://img.shields.io/badge/License-GPLv3-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.22+-blue.svg)](https://golang.org)
[![Status](https://img.shields.io/badge/Status-Alpha-orange.svg)]()

---

## What is S.W.A.R.M.?

S.W.A.R.M. is a self-hosted decentralized mesh network written in Go.
It creates an encrypted tunnel between your devices and exit nodes
using QUIC transport and ChaCha20-Poly1305 encryption.

Every participant strengthens the network. No central servers.
No single point of failure.

---

## How it works

```
[Your devices — phone, laptop, TV, console]
          ↓ transparent proxy (no config needed on devices)
[S.W.A.R.M. node — your home server]
          ↓ QUIC encrypted tunnel
          ↓ ChaCha20-Poly1305 + X25519 key exchange
[Bootstrap node — VPS in another country]
          ↓
      [Internet]
```

All devices on your network are protected automatically.
No need to install anything on each device.

---

## Features

- **Zero device configuration** — set up once on your server, all devices protected
- **QUIC transport** — fast, modern, UDP-based encrypted protocol
- **ChaCha20-Poly1305** — authenticated encryption, fast on any hardware
- **X25519 + Ed25519** — modern key exchange and identity signatures
- **Transparent proxy** — intercepts traffic at OS level (TPROXY)
- **3 node modes** — bootstrap, relay, client
- **2-hop relay** — Client → Relay → Bootstrap → Internet
- **Traffic monitoring** — daily/monthly counters with satellite ISP support
- **RTT latency probe** — monitors tunnel quality every 30 seconds
- **Web dashboard** — real-time stats on port 8081
- **Satellite optimized** — BBR congestion control, large TCP buffers, DNS cache
- **Open source** — GPL v3, verify everything yourself

---

## Quick Start

### Requirements

- Linux (Ubuntu 20.04+ / Debian 12+)
- Go 1.22+
- Root access (for TPROXY)

### Bootstrap node (VPS)

```bash
git clone https://github.com/Rockenrol2017/swarm
cd swarm

# Build
go build -o swarm-node ./cmd/swarm-node/

# Configure
cat > /etc/swarm/node-config.json << EOF
{
  "mode": "bootstrap",
  "listen_addr": ":7437",
  "status_addr": ":19090",
  "identity_file": "/etc/swarm/identity.json"
}
EOF

# Run
sudo ./swarm-node -config /etc/swarm/node-config.json
```

### Client node (home server)

```bash
cat > /etc/swarm/node-config.json << EOF
{
  "mode": "client",
  "bootstrap_addr": "YOUR_VPS_IP:7437",
  "socks5_addr": ":1090",
  "status_addr": ":19090",
  "identity_file": "/etc/swarm/identity.json",
  "traffic_file": "/var/lib/swarm/traffic.json",
  "skyedge_limit_gb": 310
}
EOF

sudo ./swarm-node -config /etc/swarm/node-config.json
```

### One-command satellite optimization

```bash
sudo bash install/optimize-satellite.sh
```

Enables BBR, increases TCP buffers to 16MB, installs dnsmasq DNS cache.

---

## Architecture

```
swarm/
├── cmd/
│   ├── swarm-node/        # Main node binary
│   └── swarm-monitor/     # Web dashboard binary
├── pkg/swarmnode/
│   ├── node.go            # Node lifecycle, peer management
│   ├── peer.go            # QUIC peer connections, relay forwarding
│   ├── socks5.go          # Built-in SOCKS5 proxy with traffic counting
│   ├── tproxy.go          # Transparent proxy (SO_TRANSPARENT)
│   ├── traffic.go         # Persistent daily/monthly traffic counters
│   ├── latency.go         # RTT probe to bootstrap node
│   ├── status.go          # HTTP status API
│   └── peers_exchange.go  # Peer discovery and exchange
├── pkg/swarmproto/
│   ├── handshake.go       # Crypto handshake: X25519 + Ed25519
│   ├── cipher.go          # ChaCha20-Poly1305 session encryption
│   └── packet.go          # Wire protocol framing
├── swarm-monitor/
│   └── index.html         # Web dashboard UI
└── install/
    ├── optimize-satellite.sh  # BBR + buffer tuning
    ├── redeploy.sh            # Build and deploy script
    └── systemd/               # Service files
```

---

## API Reference

`GET /api/status` — node status

```json
{
  "mode": "client",
  "node_id": "...",
  "uptime": "2h34m",
  "peers": 1,
  "bytes_up": 1234567,
  "bytes_down": 9876543,
  "bytes_today": 11111110,
  "bytes_month": 11111110,
  "limit_gb": 310,
  "limit_percent": 0.003,
  "latency_ms": 1450
}
```

`GET /health` — liveness check

`GET /api/peers` — connected peers list

---

## Node Modes

| Mode | Description |
|------|-------------|
| `bootstrap` | Entry point, accepts connections, proxies to internet |
| `relay` | Forwards traffic: Client → Relay → Bootstrap → Internet |
| `client` | End-user node, connects to bootstrap or relay |

---

## Security

- **ChaCha20-Poly1305** authenticated encryption on all traffic
- **X25519** ephemeral key exchange per session
- **Ed25519** node identity signatures
- **Zero logs** — no traffic content is ever logged
- **Open source** — full code audit possible

Report vulnerabilities privately — do not open public issues.
See [SECURITY.md](SECURITY.md) for details.

---

## Roadmap

- [x] QUIC transport with ChaCha20-Poly1305
- [x] Bootstrap, relay, client modes
- [x] 2-hop relay forwarding
- [x] Transparent proxy (TPROXY)
- [x] Traffic monitoring (daily/monthly)
- [x] RTT latency probe
- [x] Web dashboard
- [x] Satellite link optimization
- [ ] DHT peer discovery (no bootstrap needed)
- [ ] Android client
- [ ] Windows client
- [ ] Ad blocking (DNS-based)
- [ ] Family mode

---

## Contributing

Areas where help is needed:

- Windows / macOS client
- Android / iOS app
- Web UI improvements
- Security audit
- Documentation and translations

See [CONTRIBUTING.md](CONTRIBUTING.md) for details.

---

## License

GNU General Public License v3.0 — see [LICENSE](LICENSE)
