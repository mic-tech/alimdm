package com.alimdm

import android.content.Context
import android.provider.Settings
import android.view.accessibility.AccessibilityManager
import android.accessibilityservice.AccessibilityServiceInfo
import android.content.Intent
import com.facebook.react.bridge.*

class AccessibilityServiceModule(reactContext: ReactApplicationContext) :
    ReactContextBaseJavaModule(reactContext) {

    companion object {
        const val NAME = "AccessibilityServiceModule"
    }

    override fun getName(): String = NAME

    @ReactMethod
    fun isServiceEnabled(promise: Promise) {
        try {
            val enabled = checkServiceEnabled()
            promise.resolve(enabled)
        } catch (e: Exception) {
            promise.reject("ERROR", "Failed to check: ${e.message}")
        }
    }

    @ReactMethod
    fun openAccessibilitySettings(promise: Promise) {
        try {
            val intent = Intent(Settings.ACTION_ACCESSIBILITY_SETTINGS)
            intent.flags = Intent.FLAG_ACTIVITY_NEW_TASK
            reactApplicationContext.startActivity(intent)
            promise.resolve(true)
        } catch (e: Exception) {
            promise.reject("ERROR", "Failed to open settings: ${e.message}")
        }
    }

    private fun checkServiceEnabled(): Boolean {
        val context = reactApplicationContext
        val am = context.getSystemService(Context.ACCESSIBILITY_SERVICE) as AccessibilityManager

        val enabledServices = try {
            am.getEnabledAccessibilityServiceList(AccessibilityServiceInfo.FEEDBACK_GENERIC)
        } catch (e: Exception) {
            emptyList()
        }

        return enabledServices.any { service ->
            service.id.contains(context.packageName) &&
            service.id.contains("VolumeKeyAccessibilityService")
        }
    }
}
