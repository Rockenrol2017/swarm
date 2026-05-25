# Changelog

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
