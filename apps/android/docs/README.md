<div align="center">

# Ali MDM Documentation

**Free open-source kiosk mode for Android tablets**

_Complete guides for deployment, automation, and integration_

<p>
  <img src="https://img.shields.io/badge/License-MIT-blue.svg" alt="License: MIT">
  <img src="https://img.shields.io/badge/Android-8.0%2B-green.svg" alt="Android 8.0+">
</p>

</div>


## What is Ali MDM?

Ali MDM is a **free, open-source kiosk platform** for Android tablets. It's designed for teams and individuals who need **full device control without licensing costs**.

**Perfect for:**
- Home Assistant dashboards
- Corporate information displays
- Cloud gaming kiosks (Steam Link, Xbox Cloud Gaming)
- Digital signage
- Hotel/restaurant tablets
- Industrial control panels

### Why Choose Ali MDM?

- **100% Free** - No per-device costs, no subscriptions, MIT licensed
- **Complete Lockdown** - Device Owner mode for enterprise-grade security
- **Multiple Modes** - WebView, External App, Dashboard, Media Player
- **Rich Automation** - 40+ REST API endpoints + MQTT with Home Assistant auto-discovery
- **Mass Deployment** - ADB-based headless provisioning
- **Self-hosted** - No third-party tracking or vendor cloud. Ali MDM does talk
  to a cloud continuously, but it is *your* server: see
  [`docs/architecture.md`](../../../docs/architecture.md). Upstream FreeKiosk
  runs standalone with no server at all.

### Ali MDM vs Fully Kiosk Browser

| Feature | Ali MDM | Fully Kiosk |
|---------|:---------:|:-----------:|
| **Price** | Free | €7.90/device |
| **Open Source** | MIT | Closed |
| **Device Owner** | Yes | Yes |
| **REST API** | 40+ endpoints | Yes |
| **MQTT + HA Discovery** | Yes | No |
| **Cloud Management** | Roadmap | Yes |


## Quick Start

### Basic Installation (5 minutes)

1. Build the APK — see [`docs/howto/build-apk.md`](../../../docs/howto/build-apk.md).
   There are no published releases: this fork is signed with its own key, and
   that key is what the provisioning checksum and every over-the-air update
   depend on.
2. Install on your Android 8.0+ tablet
3. Configure URL and PIN
4. Start kiosk mode

For a managed fleet you would not do any of the above by hand — the tablet
scans a QR, installs itself and enrols. See
[`docs/howto/zero-touch-qr.md`](../../../docs/howto/zero-touch-qr.md).

### Production Deployment (Device Owner)

For complete lockdown with no system interruptions:

```bash
adb shell dpm set-device-owner com.alimdm/.DeviceAdminReceiver
```

> [!TIP]
> See the complete [Installation Guide](installation.md) for detailed setup instructions.


## Documentation Guide

### Getting Started

| Guide | Description | Link |
|-------|-------------|------|
| **Installation** | Complete setup guide from basic to Device Owner mode | [Read →](installation.md) |
| **Features & Modes** | Understand WebView, External App, Dashboard modes | [Read →](features-and-modes.md) |
| **FAQ** | Common questions and troubleshooting | [Read →](faq.md) |

### Integration & Automation

| Guide | Description | Link |
|-------|-------------|------|
| **Integrations Overview** | Choose between REST API and MQTT | [Read →](INTEGRATIONS.md) |
| **REST API** | 40+ HTTP endpoints for device control | [Read →](rest-api.md) |
| **MQTT** | Real-time telemetry and Home Assistant discovery | [Read →](MQTT.md) |
| **ADB Configuration** | Headless provisioning and scripting | [Read →](adb-configuration.md) |

### Advanced Topics

| Guide | Description | Link |
|-------|-------------|------|
| **Development** | Build and contribute to Ali MDM | [Read →](development.md) |
| **Roadmap & Changelog** | Release notes and future plans | [Read →](roadmap-and-changelog.md) |
| **Wiki Sync** | Upstream's docs-to-wiki publishing — not wired up in this fork | [Read →](pipeline-and-wiki-sync.md) |


## Common Use Cases

### Home Assistant Dashboard
```bash
# Configure via ADB
adb shell am start -n com.alimdm/.MainActivity \
    --es url "https://homeassistant.local:8123" \
    --es pin "1234" \
    --es mqtt_enabled "true" \
    --es mqtt_broker_url "192.168.1.100"
```

### Cloud Gaming Kiosk
```bash
# Lock to Steam Link with auto-relaunch
adb shell am start -n com.alimdm/.MainActivity \
    --es lock_package "com.valvesoftware.steamlink" \
    --es pin "1234" \
    --es test_mode "false" \
    --ez auto_start true
```

### Information Display
```bash
# Simple URL kiosk with REST API
adb shell am start -n com.alimdm/.MainActivity \
    --es url "https://dashboard.company.com" \
    --es pin "0000" \
    --es rest_api_enabled "true" \
    --es rest_api_port "8080"
```


## Resources

- **Website:** [github.com/mic-tech/alimdm](https://github.com/mic-tech/alimdm)
- **Releases:** [GitHub Releases](https://github.com/mic-tech/alimdm/releases)
- **Issues:** [Report Bugs](https://github.com/mic-tech/alimdm/issues)
- **Discussions:** [Community Forum](https://github.com/mic-tech/alimdm/discussions)
- **Support:** support@example.com


## Contributing

Ali MDM is open source and welcomes contributions!

- **Code:** [Contributing Guide](https://github.com/mic-tech/alimdm/blob/main/CONTRIBUTING.md)
- **Documentation:** Submit PRs to improve these docs
- **Feedback:** Share your use case in [Discussions](https://github.com/mic-tech/alimdm/discussions)


<div align="center">

**Made with ❤️ by [Rushb](https://rushb.fr)**

</div>
