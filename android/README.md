# S.W.A.R.M. for Android

Android client for the S.W.A.R.M. mesh network. Runs as a background VPN service, routing all device traffic through the encrypted swarm.

## Status

| Component | Status |
|-----------|--------|
| Core Go library (`pkg/swarmnode`) | ✅ Ready |
| Android VPN wrapper (Kotlin) | 🔧 In development |
| UI (Jetpack Compose) | 📋 Planned |
| Play Store / F-Droid | 📋 Planned |

> **Current workaround:** Android devices can use S.W.A.R.M. today via a WiFi router running the client node. See [Setup: Router Path](../docs/SETUP-ROUTER.md).

## How it works

The Android app uses the [Android VpnService API](https://developer.android.com/reference/android/net/VpnService) to create a local TUN interface. All device traffic is intercepted at the OS level and forwarded to the swarm node running inside the app process.

```
App traffic (any app)
      ↓  (VpnService TUN fd)
S.W.A.R.M. core (gomobile/cgo)
      ↓  QUIC + ChaCha20-Poly1305
Bootstrap VDS
      ↓
Internet
```

No root required. Works on Android 6.0+.

## Technology stack

| Layer | Technology |
|-------|-----------|
| VPN tunnel | Android VpnService API (Kotlin) |
| Core routing | Go (via gomobile or JNI) |
| Transport | QUIC (quic-go) |
| Crypto | ChaCha20-Poly1305 + X25519 |
| UI | Jetpack Compose |
| Background | Foreground Service + WorkManager |

## Planned modes

### Home mode
When connected to home WiFi:
- Full SOCKS5 proxy through swarm (same as desktop client)
- Background service, minimal battery use
- Useful when router does not support TPROXY

### Street mode (mobile data / other WiFi)
- VpnService routes all traffic through swarm
- Automatic reconnect on network change
- Battery-aware: reduces relay activity when battery < 20%

### Charging mode
- Becomes a relay node: contributes bandwidth to the swarm
- Only active when charging + on WiFi
- Respects `max_relay_percent` setting (default 20%)

## Build

> Requires Android NDK + gomobile setup. Instructions pending.

```bash
# Install gomobile
go install golang.org/x/mobile/cmd/gomobile@latest
gomobile init

# Build AAR library from swarmnode package
gomobile bind -target android -o swarm-core.aar \
    github.com/narodnaya-set/swarm/pkg/swarmnode

# Then build the Android project in android/ with Gradle
```

## Contributing

The Android app is the next major milestone. Contributions welcome:
- Kotlin/Android developers
- gomobile / JNI experience a plus
- UI/UX designers

Open an issue or email: semenov2298@gmail.com

## See also

- [Architecture overview](ARCHITECTURE.md)
- [Router setup path](../docs/SETUP-ROUTER.md) — use swarm today without the app
- [Main README](../README.md)
