# Building the Ali MDM APK

You need the Ali MDM APK to install on the tablets. Two ways to get it:

## Option 1 — Download the official release (easiest, recommended to start)

Ali MDM publishes signed release APKs on GitHub:

1. Go to **https://github.com/RushB-fr/alimdm/releases**
2. Download the latest **`Ali MDM-vX.X.X.apk`** (the Android one).
3. That's your APK — use it with `enroll-one.sh` / `adb install`.

> This is the stock app. It already has the cloud client enabled, so it works
> with our server out of the box. Start here.

## Option 2 — Build your own (if you want to customize)

Only do this if you need to change something (add a logo, tweak defaults, etc.).
Building requires the **Android SDK** + **JDK 17** + **Node**.

### Prerequisites
```bash
# JDK 17
# Android SDK (set ANDROID_HOME)
# Node 18+
echo $ANDROID_HOME   # should be set
```

### Build
```bash
cd alimdm/android
./gradlew assembleRelease
```

The signed release APK lands at:
```
alimdm/android/app/build/outputs/apk/release/app-release.apk
```

### Signing
`assembleRelease` needs a signing config. Either:
- Use your own keystore (set `signingConfigs.release` in
  `android/app/build.gradle`), or
- Build a **debug** APK for testing (auto-signed, not for production):
  ```bash
  ./gradlew assembleDebug
  # -> android/app/build/outputs/apk/debug/app-debug.apk
  ```
  Debug APKs work for testing enrollment but aren't suitable for a permanent
  production deployment (they expire / can't be updated cleanly).

> **Recommendation:** use the **official release APK** (Option 1) for your
> school. It's signed, tested, and already has the cloud client. Building your
> own only makes sense if you're modifying the app.

## Where to keep the APK

Keep a copy somewhere handy for enrollment, e.g.:
```
~/tablets/alimdm-release.apk
```
Then:
```bash
./docs/howto/enroll-one.sh https://cloud.yourdomain.com "$ALIMDM_ENROLL_TOKEN" ~/tablets/alimdm-release.apk
```

## Hosting the APK (for the full zero-touch QR path)

If you later want the QR to install the app automatically, host the APK at a
public HTTPS URL on your VPS, e.g. `https://cloud.yourdomain.com/apk/alimdm.apk`.
(Ask me to wire that up — it's a small addition to the server + QR generator.)

## Note on the pre-downloaded APK

A copy of the official release APK is kept locally at `apk/alimdm-v1.2.20-beta.6.apk`
for testing, but it is **not committed to git** (it's a 61 MB binary — the repo holds
source only). To re-download it anytime:

```bash
mkdir -p apk && cd apk
curl -L -o alimdm-v1.2.20-beta.6.apk \
  "https://github.com/RushB-fr/alimdm/releases/download/v1.2.20-beta.6/alimdm-v2.2.20-beta.6.apk"
```
