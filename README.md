# S.W.A.R.M.
### Secure · Worldwide · Anonymous · Routing · Mesh

> A decentralized peer-to-peer encrypted mesh network.  
> **The more nodes — the faster and stronger the network for everyone.**

[![Release](https://img.shields.io/github/v/release/Rockenrol2017/swarm?color=brightgreen)](https://github.com/Rockenrol2017/swarm/releases/latest)
[![Build](https://img.shields.io/github/actions/workflow/status/Rockenrol2017/swarm/release.yml?label=build)](https://github.com/Rockenrol2017/swarm/actions)
[![License: GPL v3](https://img.shields.io/badge/License-GPLv3-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.22+-blue.svg)](https://golang.org)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](CONTRIBUTING.md)

---

## 🌍 Run a Node — Help the Network

> **Got a VPS? One command is all it takes.**

Every bootstrap node you run makes the entire swarm faster and more reliable for everyone.  
No configuration needed. No maintenance. Just run and forget.

```bash
curl -sSL https://raw.githubusercontent.com/Rockenrol2017/swarm/main/install/setup-bootstrap.sh | bash
```

**Requirements:** Linux VPS (any provider) · Root access · Port 7437/UDP open · ~50 MB RAM

The script automatically installs Go, builds the node, sets up systemd, and opens firewall ports.  
After ~2 minutes your node is running and serving the swarm. 🎉

> 💡 **The more geographically diverse the nodes — the better.**  
> Frankfurt, Helsinki, Singapore, New York — every location helps.

---

## 🌐 Live Network

The swarm is already running. Connect your client to any of these bootstrap nodes:

| Location | Status |
|----------|--------|
| 🇸🇪 Stockholm, Sweden | ✅ Active |
| 🇩🇪 Frankfurt, Germany | ✅ Active |
| 🇺🇸 New York, USA | ✅ Active |

Bootstrap addresses are included automatically when you install with `install.sh`.  
The more nodes — the more resilient and faster the network for everyone.

📊 **[Live network status →](https://stats.uptimerobot.com/p5plfaprdV)**

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

### Bootstrap node (VPS) — one command

```bash
curl -sSL https://raw.githubusercontent.com/Rockenrol2017/swarm/main/install.sh | bash
```

No Go required — downloads a pre-built binary, sets up systemd, opens firewall ports automatically.  
At the end it prints your node's IP and NodeID — add them to client configs to join the swarm.

> **Prefer to build from source?**
> ```bash
> curl -sSL https://raw.githubusercontent.com/Rockenrol2017/swarm/main/install/setup-bootstrap.sh | bash
> ```

### Client node (home server)

```bash
# Install
curl -sSL https://raw.githubusercontent.com/Rockenrol2017/swarm/main/install.sh | bash

# Edit config
nano /etc/swarm/node-config.json
# Set: "mode": "client", "bootstrap_addr": "YOUR_VPS_IP:7437"

# Restart
systemctl restart swarm-node
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

**The easiest way to contribute: run a bootstrap node** (see above ↑).  
Every node makes the network stronger.

Other areas where help is needed:

- 🖥️ Windows / macOS native client
- 📱 Android / iOS app
- 🌐 Web UI improvements
- 🔐 Security audit
- 📖 Documentation and translations
- 🌍 Run nodes in underrepresented regions (Asia, South America, Africa)

See [CONTRIBUTING.md](CONTRIBUTING.md) for details.

---

## License

GNU General Public License v3.0 — see [LICENSE](LICENSE)
