# r/selfhosted / r/homelab post (English)

**Title:** I built a self-hosted P2P encrypted mesh network in Go — 3 VPS nodes, satellite link, whole-home routing

---

Hey r/selfhosted!

I've been working on **S.W.A.R.M.** — a decentralized P2P mesh network written in Go that routes all traffic from every device on your home network through encrypted tunnels to exit nodes worldwide.

**The problem I was solving:** I'm on a satellite ISP (SkyEdge, ~1800ms RTT). Every VPN solution I tried either had terrible performance on high-latency links or required configuring each device individually. I wanted something that:
- Runs once on my home server, protects every device automatically
- Works well on high-latency links
- Doesn't require any cloud provider lock-in

**What I built:**

```
[Phone, laptop, TV, console]
        ↓ transparent proxy (zero config on devices)
[Home server — S.W.A.R.M. client node]
        ↓ QUIC · ChaCha20-Poly1305 · X25519
[Bootstrap node — VPS in another country]
        ↓
    [Internet]
```

**Tech stack:**
- **QUIC transport** (quic-go) — handles satellite latency well, BBR congestion control
- **ChaCha20-Poly1305** authenticated encryption on all traffic
- **X25519 + Ed25519** key exchange and identity
- **Transparent proxy** via TPROXY — intercepts at OS level, no per-device config
- **3 node modes:** bootstrap (VPS), relay (2-hop), client (home server)

**Live network:** 3 bootstrap nodes running across 3 continents (Stockholm 🇸🇪, Frankfurt 🇩🇪, New York 🇺🇸). Confirmed working — `curl https://api.ipinfo.io/ip` from Windows through the swarm → VDS IP.

**One-command install for a new bootstrap node:**
```bash
curl -sSL https://raw.githubusercontent.com/Rockenrol2017/swarm/main/install.sh | bash
```

**Network status:** https://stats.uptimerobot.com/p5plfaprdV

**GitHub:** https://github.com/Rockenrol2017/swarm

Happy to answer questions about the architecture, QUIC on satellite links, or anything else. The more bootstrap nodes the network has, the better it works for everyone — if you've got a spare VPS, it takes 1 minute to join.

---

*Tags: go, p2p, mesh, quic, encrypted, self-hosted, vpn-alternative*
