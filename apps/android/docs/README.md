<div align="center">

# Ali MDM Documentation

**Free open-source kiosk mode for Android tablets**

_Complete guides for deployment, automation, and integration_

<p>
  <img src="https://img.shields.io/badge/Version-1.2.17-blue.svg" alt="Version 1.2.17">
  <a href="https://github.com/mic-tech/alimdm/releases"><img src="https://img.shields.io/github/downloads/mic-tech/alimdm/total.svg" alt="Downloads"></a>
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
- **Privacy First** - No tracking, no cloud dependencies

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

1. Download the latest APK from [**Releases**](https://github.com/mic-tech/alimdm/releases)
2. Install on your Android 8.0+ tablet
3. Configure URL and PIN
4. Start kiosk mode

### Production Deployment (Device Owner)

For complete lockdown with no system interruptions:

```bash
adb shell dpm set-device-owner com.alimdm/.DeviceAdminReceiver
```

> [!TIP]
> See the complete [Installation Guide](Installation) for detailed setup instructions.


## Documentation Guide

### Getting Started

| Guide | Description | Link |
|-------|-------------|------|
| **Installation** | Complete setup guide from basic to Device Owner mode | [Read →](Installation) |
| **Features & Modes** | Understand WebView, External App, Dashboard modes | [Read →](Features-and-Modes) |
| **FAQ** | Common questions and troubleshooting | [Read →](FAQ) |

### Integration & Automation

| Guide | Description | Link |
|-------|-------------|------|
| **Integrations Overview** | Choose between REST API and MQTT | [Read →](Integrations) |
| **REST API** | 40+ HTTP endpoints for device control | [Read →](REST-API) |
| **MQTT** | Real-time telemetry and Home Assistant discovery | [Read →](MQTT) |
| **ADB Configuration** | Headless provisioning and scripting | [Read →](ADB-Configuration) |

### Advanced Topics

| Guide | Description | Link |
|-------|-------------|------|
| **Development** | Build and contribute to Ali MDM | [Read →](Development) |
| **Roadmap & Changelog** | Release notes and future plans | [Read →](Roadmap-and-Changelog) |
| **Wiki Sync** | How documentation is published | [Read →](Pipeline-and-Wiki-Sync) |


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
