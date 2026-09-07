# Building the Ali MDM APK

There is no release to download. Ali MDM is a private fork of FreeKiosk with
its own signing key, and the key is what everything else hangs off: the QR's
`ALIMDM_PROVISION_CHECKSUM` is the SHA-256 of that certificate, and Android
refuses an update signed by anything else. An upstream FreeKiosk release would
install, fail the provisioning checksum, and never be able to update a tablet
already running your build. Build it here.

Building requires the **Android SDK**, **JDK 17** and **Node 20+**.

### Prerequisites
```bash
echo $ANDROID_HOME   # should be set
java -version        # 17
```

### Build
```bash
cd apps/android/android
./gradlew assembleRelease
```

The signed release APK lands at:
```
apps/android/android/app/build/outputs/apk/release/app-release.apk
```

### Signing

`assembleRelease` reads `apps/android/android/keystore.properties`, which names
a keystore kept outside the repo. See `keystore.properties.example`. If that
file is absent the build falls back to the debug keystore; if it is present but
does not name a keystore the build fails on purpose, because silently producing
a debug-signed APK gives you something that installs fine and can then never
upgrade a single release-signed tablet.

**Back the keystore up somewhere off this machine.** Losing it means no tablet
you have already shipped can ever be updated again.

For throwaway testing only:
```bash
./gradlew assembleDebug
# -> app/build/outputs/apk/debug/app-debug.apk
```

## Getting it onto tablets

**By QR (no cable).** Upload the APK on the console's **App update** page. That
staged build is what a scanned QR downloads and installs, and what an over-the-air
rollout sends to tablets already enrolled. This is wired up and is how the fleet
is built — see [`zero-touch-qr.md`](zero-touch-qr.md).

**By cable.** Keep a copy handy and pass it to the enrol script:
```bash
./docs/howto/enroll-one.sh https://cloud.yourdomain.com "$ALIMDM_ENROLL_TOKEN" ~/tablets/alimdm-release.apk
```

## After changing signing keys

If you ever sign with a different key, the provisioning checksum must change
with it or QR enrolment fails *after* the tablet has downloaded the APK — which
looks like a network fault. Regenerate it as described in
[`zero-touch-qr.md`](zero-touch-qr.md), and remember that existing tablets
cannot be updated across a key change at all.
