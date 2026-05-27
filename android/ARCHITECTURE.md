# S.W.A.R.M. Android — Architecture

## Overview

The Android client integrates the S.W.A.R.M. Go core into an Android app via gomobile (or JNI). The app creates a VPN tunnel using the Android VpnService API and routes all device traffic through the swarm.

## VpnService flow

```
┌─────────────────────────────────────────┐
│              Android App                │
│                                         │
│  ┌──────────────┐   ┌────────────────┐  │
│  │ Jetpack UI   │   │ ForegroundSvc  │  │
│  │ (Compose)    │   │ SwarmVpnSvc    │  │
│  └──────┬───────┘   └───────┬────────┘  │
│         │ start/stop        │           │
│         └──────────────────►│           │
│                             │           │
│                    ┌────────▼─────────┐ │
│                    │  VpnService API  │ │
│                    │  (TUN fd)        │ │
│                    └────────┬─────────┘ │
│                             │           │
│                    ┌────────▼─────────┐ │
│                    │  Go core         │ │
│                    │  (gomobile AAR)  │ │
│                    │  swarmnode.Node  │ │
│                    └────────┬─────────┘ │
└─────────────────────────────┼───────────┘
                              │ QUIC UDP
                    ┌─────────▼─────────┐
                    │  Bootstrap VDS    │
                    │  :7437            │
                    └─────────┬─────────┘
                              │
                          Internet
```

## Components

### SwarmVpnService (Kotlin)

`app/src/main/kotlin/net/narodnaya/swarm/SwarmVpnService.kt`

Extends `android.net.VpnService`. Responsibilities:
1. Create TUN interface via `VpnService.Builder`
2. Exclude swarm's own QUIC traffic from the tunnel (to avoid routing loop)
3. Pass TUN file descriptor to Go core
4. Handle network change broadcasts (reconnect on WiFi↔LTE switch)
5. Run as foreground service with persistent notification

```kotlin
class SwarmVpnService : VpnService() {
    private var swarmNode: SwarmNode? = null  // Go binding

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        val tun = Builder()
            .addAddress("10.99.0.1", 30)
            .addRoute("0.0.0.0", 0)           // route all IPv4
            .addRoute("::", 0)                 // route all IPv6
            .addDnsServer("1.1.1.1")
            .setMtu(1400)
            .establish()!!

        // Start Go core with TUN fd + config
        swarmNode = SwarmNode.newClientNode(config)
        swarmNode?.startWithTUN(tun.detachFd())
        return START_STICKY
    }
}
```

### Go core integration

The `pkg/swarmnode` package is compiled as an Android library via gomobile. Key exported interface:

```go
// swarmnode_mobile.go — gomobile-compatible exports
package swarmnode

// MobileNode — thin wrapper for gomobile binding.
type MobileNode struct {
    node *Node
}

func NewClientNode(configJSON string) (*MobileNode, error)
func (m *MobileNode) StartWithTUN(tunFD int) error
func (m *MobileNode) Stop()
func (m *MobileNode) StatusJSON() string   // returns NodeStatus as JSON
func (m *MobileNode) SetMaxRelayPercent(pct int)
```

### Network change handling

Android frequently changes networks (WiFi ↔ LTE, hotspot, VPN handoff). The service listens to `ConnectivityManager.NetworkCallback` and triggers reconnect:

```kotlin
val callback = object : NetworkCallback() {
    override fun onAvailable(network: Network) {
        // Give Go core 1s to detect the disconnect, then reconnect
        handler.postDelayed({ swarmNode?.reconnect() }, 1000)
    }
}
```

QUIC's connection migration (RFC 9000) handles most cases automatically — the swarm connection survives network changes without full reconnect.

## Traffic flow (TUN mode)

```
App (e.g. browser)
  → Android network stack
  → TUN interface (fd from VpnService)
  → Go core reads TUN packets
  → Wrapped in QUIC stream to bootstrap
  → Bootstrap forwards to Internet
  → Response returns via same QUIC stream
  → Go core writes to TUN
  → App receives response
```

For DNS: Go core intercepts UDP port 53 and uses a built-in resolver to avoid DNS leaks.

## Battery and relay policy

| Condition | Relay active | Max relay % |
|-----------|-------------|-------------|
| Charging + WiFi | Yes | cfg.MaxRelayPercent (20%) |
| Battery < 20% | No | 0% |
| Mobile data | No | 0% |
| WiFi, battery OK | Optional | cfg.MaxRelayPercent |

Relay activity is controlled via `Node.cfg.MaxRelayPercent`. The Kotlin layer sets this based on battery/network state via `MobileNode.SetMaxRelayPercent(pct)`.

## Modes

### Home WiFi mode
- VpnService routes all traffic through swarm
- Identical to desktop client mode
- Uses existing bootstrap connections (latency-first routing)

### Street mode (away from home)
- Same as home mode but on mobile data
- Traffic monitoring: SkyEdge-style limit can be configured
- Auto-reconnect on cell tower handoff

### Relay/charging mode
- Node contributes bandwidth to the swarm as a relay
- Only when: charging + on WiFi + battery > 80%
- Announced via MsgRelayReady to bootstrap

## Security considerations

- **No root required** — VpnService API is unprivileged
- **Split tunneling** — QUIC traffic to bootstrap is excluded from TUN to prevent loop
- **DNS leak prevention** — DNS is resolved via swarm tunnel
- **Identity** — same X25519 + Ed25519 identity as desktop nodes, stored in app private storage
- **Certificate pinning** — bootstrap NodeID verified during QUIC handshake (existing mechanism)

## Build prerequisites

```
Android Studio Hedgehog or later
Android NDK r26+
Go 1.22+
gomobile (golang.org/x/mobile)
```

Target: Android 6.0 (API 23) minimum, tested on Android 12+.

## Directory structure (planned)

```
android/
  app/
    src/main/
      kotlin/net/narodnaya/swarm/
        SwarmVpnService.kt
        MainActivity.kt
        StatusViewModel.kt
      res/
        layout/
        drawable/
  swarm-core/           ← gomobile AAR output
  build.gradle
  README.md             ← this file's parent
  ARCHITECTURE.md       ← this file
```
