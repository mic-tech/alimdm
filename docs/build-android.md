# Building the Ali MDM app

Verified on Ubuntu with a cold toolchain: `BUILD SUCCESSFUL in 4m 25s`.

## Prerequisites

| Need | Why |
|---|---|
| **JDK 17** | AGP rejects newer JDKs. If `java -version` reports 21+, set `JAVA_HOME` explicitly — see below. |
| Node 20+ | Metro bundles the JS. |
| ~6 GB free | 2.8 GB SDK (2 GB of it NDK) + 350 MB `node_modules` + build output. |

## One-time SDK setup

The versions below are pinned by `apps/android/android/build.gradle`; installing
anything else will fail the build.

```bash
export ANDROID_HOME=$HOME/android-sdk
mkdir -p "$ANDROID_HOME/cmdline-tools"

curl -sL -o /tmp/cmdtools.zip \
  https://dl.google.com/android/repository/commandlinetools-linux-13114758_latest.zip
unzip -q /tmp/cmdtools.zip -d /tmp/cmdt
mv /tmp/cmdt/cmdline-tools "$ANDROID_HOME/cmdline-tools/latest"

yes | "$ANDROID_HOME/cmdline-tools/latest/bin/sdkmanager" --licenses
"$ANDROID_HOME/cmdline-tools/latest/bin/sdkmanager" \
  "platform-tools" "platforms;android-36" "build-tools;36.0.0" \
  "ndk;27.1.12297006" "cmake;3.22.1"

echo "sdk.dir=$ANDROID_HOME" > apps/android/android/local.properties
```

`local.properties` is machine-specific and gitignored.

## Build

```bash
cd apps/android
npm install                     # applies patches/ via postinstall

cd android
export JAVA_HOME=/usr/lib/jvm/java-17-openjdk-amd64
export ANDROID_HOME=$HOME/android-sdk
./gradlew assembleDebug         # app/build/outputs/apk/debug/app-debug.apk
./gradlew assembleRelease       # app/build/outputs/apk/release/app-release.apk
```

A cold debug build takes ~4-5 minutes, most of it CMake/NDK compiling native
code for four ABIs. Incremental rebuilds are ~20 seconds.

## Release signing

`android/gradle.properties` is tracked and holds build flags only, so a fresh
clone builds without setup. Signing credentials go in `android/keystore.properties`,
which is gitignored — copy `keystore.properties.example` and fill it in.

Without that file, release builds **silently fall back to the debug keystore**.
A debug-signed APK installs fine but cannot upgrade a release-signed install, so
check it before distributing:

```bash
apksigner verify --print-certs app-release.apk
```

## Verifying a build

Confirm identity from the artifact rather than trusting the build:

```bash
$ANDROID_HOME/build-tools/36.0.0/aapt2 dump badging app-debug.apk \
  | grep -E "^package: name|application: label"
# package: name='com.alimdm' ...
# application: label='Ali MDM' icon='res/mipmap-anydpi-v26/ic_launcher.xml'
```

## Note on the package name

`com.alimdm` is what Android binds Device Owner to. Changing it again would make
the app a different app to the system, requiring a factory reset and re-enrolment
of every tablet. Treat it as fixed.
