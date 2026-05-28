#!/bin/bash
# Скрипт создания GitHub Issues для v0.2.0
# Запускать: GITHUB_TOKEN=xxx bash create-issues.sh
# Токен: https://github.com/settings/tokens → repo scope

REPO="Rockenrol2017/swarm"
TOKEN="${GITHUB_TOKEN}"

create_issue() {
    local title="$1"
    local body="$2"
    local labels="$3"

    curl -s -X POST \
        -H "Authorization: token $TOKEN" \
        -H "Accept: application/vnd.github.v3+json" \
        "https://api.github.com/repos/$REPO/issues" \
        -d "{\"title\": $(echo "$title" | python3 -c 'import json,sys; print(json.dumps(sys.stdin.read().strip()))'), \"body\": $(echo "$body" | python3 -c 'import json,sys; print(json.dumps(sys.stdin.read().strip()))'), \"labels\": $labels}" \
        | python3 -c 'import json,sys; d=json.load(sys.stdin); print(f"Created #{d[\"number\"]}: {d[\"title\"]}")'
}

echo "Creating v0.2.0 milestone issues..."

create_issue \
    "feat: DHT peer discovery (Kademlia)" \
    "## Summary
Replace fixed bootstrap addresses with Kademlia DHT for fully decentralized peer discovery.

## Motivation
Currently nodes require hard-coded bootstrap addresses. If all bootstrap nodes go down, new nodes can't join the network. DHT solves this.

## Scope
- Implement Kademlia DHT routing table
- Bootstrap via any known node, not just fixed addresses
- Peer lookup: find nodes near a target ID
- Node announcements: register in DHT on startup
- Compatible with existing peer exchange protocol

## References
- Kademlia paper: https://pdos.csail.mit.edu/~petar/papers/maymounkov-kademlia-lncs.pdf
- libp2p/kad-dht for inspiration

## Acceptance criteria
- [ ] Node can join network knowing only 1 peer address
- [ ] Peer discovery works without configured bootstrap_addr
- [ ] Backward compatible with v0.1.x nodes" \
    '["enhancement","v0.2.0"]'

create_issue \
    "feat: multi-hop circuit builder (onion routing)" \
    "## Summary
Implement explicit circuit building: Client → Relay → Bootstrap → Internet with path selection.

## Motivation
Currently traffic goes Client → Bootstrap directly (1 hop). Multi-hop improves anonymity and allows geographic routing.

## Scope
- Circuit negotiation protocol (extend/created cells)
- Path selection algorithm (avoid same AS, geographic diversity)
- Layered encryption per hop
- Circuit teardown on peer failure
- Minimum 2-hop, configurable max hops

## Acceptance criteria
- [ ] Client can specify circuit length (2-3 hops)
- [ ] Traffic routed through intermediate relay nodes
- [ ] Each relay knows only previous and next hop
- [ ] Failover: rebuild circuit on hop failure" \
    '["enhancement","v0.2.0"]'

create_issue \
    "feat: Android client app" \
    "## Summary
Native Android client with VPN service integration.

## Motivation
Mobile devices are a primary use case. Currently only CLI on Linux.

## Scope
- Android VPN service (VpnService API)
- WireGuard-style tunnel interface
- Connection to swarm bootstrap nodes
- Status UI: connected/disconnected, bytes transferred, current exit IP
- Auto-connect on launch option
- Notification with current IP

## Tech options
- Go mobile (gomobile) — reuse swarmnode package
- Kotlin + JNI bridge to Go library

## Acceptance criteria
- [ ] APK installable without Play Store
- [ ] Connects to existing bootstrap nodes
- [ ] All device traffic routed through swarm
- [ ] Battery-friendly keepalive" \
    '["enhancement","v0.2.0","mobile"]'

create_issue \
    "feat: Windows GUI client" \
    "## Summary
Native Windows client with system tray icon and one-click connect.

## Motivation
Most users are on Windows. Current solution requires Linux home server.

## Scope
- System tray icon (green=connected, red=disconnected)
- WinTun or similar virtual adapter
- Quick connect/disconnect
- Exit node selection
- Status: uptime, bytes, current IP

## Tech options
- Go + systray library + WinTun
- Electron (heavier but simpler UI)

## Acceptance criteria
- [ ] MSI installer
- [ ] Connects to bootstrap nodes
- [ ] Routes all traffic through swarm
- [ ] Auto-start with Windows option" \
    '["enhancement","v0.2.0","windows"]'

create_issue \
    "feat: DNS-based ad blocking on bootstrap nodes" \
    "## Summary
Optional DNS filtering on bootstrap/exit nodes to block ads and trackers network-wide.

## Motivation
Since all DNS goes through the swarm, bootstrap nodes can optionally filter DNS responses.

## Scope
- Embedded DNS resolver on bootstrap nodes (opt-in)
- Blocklist support (hosts format, compatible with Pi-hole lists)
- Auto-update blocklists (Steven Black, AdGuard, etc.)
- Per-session opt-in/opt-out
- Metrics: blocked queries count

## Acceptance criteria
- [ ] Bootstrap node config: dns_blocking: true/false
- [ ] Default popular blocklists included
- [ ] Client sees blocked domains as NXDOMAIN
- [ ] No performance regression on non-blocked queries" \
    '["enhancement","v0.2.0"]'

create_issue \
    "feat: web admin panel (replace swarm-monitor)" \
    "## Summary
Modern web UI replacing the current swarm-monitor binary with built-in admin panel.

## Motivation
Currently swarm-monitor is a separate binary. Consolidate into swarm-node.

## Scope
- Embed web UI into swarm-node (no separate binary)
- Real-time peer map (world map with node locations)
- Traffic charts (last 24h, 30d)
- Peer management (disconnect, block)
- Config editor with validation
- Log viewer

## Acceptance criteria
- [ ] Accessible at :19090/ui
- [ ] No separate binary needed
- [ ] Works on mobile browsers
- [ ] Dark mode (match GitHub style)" \
    '["enhancement","v0.2.0","ui"]'

echo "Done!"
