package com.alimdm

import android.content.Intent
import android.net.Uri
import android.os.Build
import android.provider.Settings
import com.facebook.react.bridge.*

class OverlayPermissionModule(reactContext: ReactApplicationContext) :
    ReactContextBaseJavaModule(reactContext) {

    companion object {
        const val NAME = "OverlayPermissionModule"
    }

    override fun getName(): String = NAME

    @ReactMethod
    fun canDrawOverlays(promise: Promise) {
        try {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M) {
                val canDraw = Settings.canDrawOverlays(reactApplicationContext)
                promise.resolve(canDraw)
            } else {
                // Avant Android M, la permission est accordée automatiquement
                promise.resolve(true)
            }
        } catch (e: Exception) {
            promise.reject("ERROR", "Failed to check overlay permission: ${e.message}")
        }
    }

    @ReactMethod
    fun requestOverlayPermission(promise: Promise) {
        try {
            if (Build.VERSION.SDK_INT < Build.VERSION_CODES.M ||
                Settings.canDrawOverlays(reactApplicationContext)
            ) {
                promise.resolve(true)
                return
            }
            // Through SettingsLauncher: in lock task a plain startActivity is
            // refused without a word, so this button did nothing on a kiosk
            // device — which is every device this runs on.
            SettingsLauncher.launch(
                reactApplicationContext, promise,
                Intent(
                    Settings.ACTION_MANAGE_OVERLAY_PERMISSION,
                    Uri.parse("package:${reactApplicationContext.packageName}"),
                ),
                Intent(Settings.ACTION_MANAGE_OVERLAY_PERMISSION),
            )
        } catch (e: Exception) {
            promise.reject("ERROR", "Failed to request overlay permission: ${e.message}")
        }
    }
}
