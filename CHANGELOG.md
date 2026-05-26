# Changelog

## [0.1.1] — 2026-05-26

### Fixed
- `setup-bootstrap.sh` now syncs system clock before starting (NTP clock skew > 1m30s caused handshake failure on fresh VPS)
- `setup-bootstrap.sh` uses `git reset --hard origin/main` instead of `git pull` (handles force push)
- `update-vds.sh` now copies tproxy scripts to `/usr/local/share/swarm/` on each update
- Example configs updated with current bootstrap IPs

### Added
- `configs/` directory with example configs for all node modes (bootstrap, relay, client, client-multi-bootstrap)
- Relay mode end-to-end tested: Client → Relay → Bootstrap → Internet chain confirmed

---

## [0.1.0] — 2026-05-26

### Added
- Pre-built binaries for all platforms via GitHub Actions CI/CD
- `install.sh` — one-command install without Go (auto-detects OS/arch)
- `setup-bootstrap.sh` — one-command new VDS bootstrap setup
- `update-vds.sh` — git pull + rebuild + restart on existing VDS
- MsgPing keepalive every 25s — prevents QUIC idle disconnects
- `BootstrapNodeID` verification — anti-spoofing protection
- Multiple bootstrap support via `bootstrap_addrs[]` config field
- TPROXY traffic counting (bytes_up/bytes_down via TPROXY path)
- TPROXY auto-restore on service restart via systemd ExecStartPre

### Fixed
- `HandshakeIdleTimeout` set to 30s (default 5s too short for satellite, 3 RTT × 1900ms = 5.7s)
- `MaxIdleTimeout` increased to 120s (keepalive pings every 25s)
- `tproxy-rules.sh` now skips bootstrap/relay nodes (only needed on client)
- `tproxy.go` build tag `//go:build linux` — compiles on macOS/Windows too
- `update-vds.sh` uses `git reset --hard origin/main` (handles force push)
- bytes_up/bytes_down now persist across restarts (saved to traffic.json)

---

## [0.1.0-alpha] — 2026-05-25

### Added

- QUIC transport with ChaCha20-Poly1305 encryption
- X25519 key exchange + Ed25519 node identity
- Three node modes: bootstrap, relay, client
- 2-hop relay forwarding (Client → Relay → Bootstrap → Internet)
- Built-in SOCKS5 proxy with traffic counting
- Transparent proxy via TPROXY (SO_TRANSPARENT, cap_net_admin)
- Persistent traffic counters (daily/monthly) saved to JSON
- RTT latency probe to bootstrap node every 30 seconds
- Web dashboard (swarm-monitor) on port 8081
- HTTP status API on port 19090
- Satellite link optimization script (BBR, TCP 16MB buffers, dnsmasq)
- Systemd service files with auto-restart
- One-command installation script

### Fixed

- nil context in OpenStreamSync (peers_exchange.go)
- Relay mode not connecting to upstream bootstrap
- redeploy.sh missing setcap after binary replacement

### Known Issues

- DNS UDP via TPROXY may occasionally drop on high-latency links
  Workaround: add RETURN rule for UDP port 53 in TPROXY chain
