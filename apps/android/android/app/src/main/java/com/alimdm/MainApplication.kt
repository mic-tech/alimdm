package com.alimdm

import android.app.Application
import com.facebook.react.PackageList
import com.facebook.react.ReactApplication
import com.facebook.react.ReactHost
import com.facebook.react.ReactNativeApplicationEntryPoint.loadReactNative
import com.facebook.react.defaults.DefaultReactHost.getDefaultReactHost
import com.alimdm.api.HttpServerPackage
import com.alimdm.mqtt.MqttPackage

class MainApplication : Application(), ReactApplication {

  override val reactHost: ReactHost by lazy {
    getDefaultReactHost(
      context = applicationContext,
      packageList =
        PackageList(this).packages.apply {
          // Packages that cannot be autolinked yet can be added manually here
          add(KioskPackage())
          add(CertificatePackage())
          add(MotionDetectionPackage())
          add(AppLauncherPackage())
          add(OverlayPermissionPackage())
          add(LauncherPackage())
          add(OverlayServicePackage())
          add(SystemInfoPackage())
          add(UpdatePackage())
          add(ManagedAppInstallerPackage())
          add(AgentUpdatePackage())
          add(SoundPlayerPackage())
          add(HttpServerPackage())
          add(MqttPackage())
          add(BlockingOverlayPackage())
          add(AutoBrightnessPackage())
          add(PrintPackage())
          add(AccessibilityPackage())
          add(FilePickerPackage())
          add(WifiControlPackage())
          add(BluetoothControlPackage())
          add(AudioControlPackage())
          add(FlashlightPackage())
          add(RotationControlPackage())
        },
    )
  }

  override fun onCreate() {
    super.onCreate()
    loadReactNative(this)
  }
}
