package com.alimdm

import android.content.ComponentName
import android.content.Intent
import android.content.pm.PackageManager
import com.facebook.react.bridge.*

/**
 * LauncherModule - Gère l'activation/désactivation de HomeActivity comme launcher
 *
 * Permet à React Native de:
 * - Activer HomeActivity comme launcher par défaut (External App Mode)
 * - Désactiver HomeActivity pour revenir au comportement normal
 * - Ouvrir les paramètres système pour définir le launcher par défaut
 */
class LauncherModule(reactContext: ReactApplicationContext) :
    ReactContextBaseJavaModule(reactContext) {

    companion object {
        const val NAME = "LauncherModule"
    }

    override fun getName(): String = NAME

    @ReactMethod
    fun enableHomeLauncher(promise: Promise) {
        try {
            val packageManager = reactApplicationContext.packageManager
            val componentName = ComponentName(reactApplicationContext, HomeActivity::class.java)

            packageManager.setComponentEnabledSetting(
                componentName,
                PackageManager.COMPONENT_ENABLED_STATE_ENABLED,
                PackageManager.DONT_KILL_APP
            )

            DebugLog.d("LauncherModule", "HomeActivity enabled as launcher")
            promise.resolve(true)
        } catch (e: Exception) {
            DebugLog.errorProduction("LauncherModule", "Error enabling HomeActivity: ${e.message}")
            promise.reject("ERROR", "Failed to enable home launcher: ${e.message}")
        }
    }

    @ReactMethod
    fun disableHomeLauncher(promise: Promise) {
        try {
            val packageManager = reactApplicationContext.packageManager
            val componentName = ComponentName(reactApplicationContext, HomeActivity::class.java)

            packageManager.setComponentEnabledSetting(
                componentName,
                PackageManager.COMPONENT_ENABLED_STATE_DISABLED,
                PackageManager.DONT_KILL_APP
            )

            DebugLog.d("LauncherModule", "HomeActivity disabled as launcher")
            promise.resolve(true)
        } catch (e: Exception) {
            DebugLog.errorProduction("LauncherModule", "Error disabling HomeActivity: ${e.message}")
            promise.reject("ERROR", "Failed to disable home launcher: ${e.message}")
        }
    }

    @ReactMethod
    fun isHomeLauncherEnabled(promise: Promise) {
        try {
            // Vérifier si HomeActivity est activée ET si AliMDM est le launcher par défaut
            val packageManager = reactApplicationContext.packageManager
            val componentName = ComponentName(reactApplicationContext, HomeActivity::class.java)

            // 1. Vérifier si HomeActivity est activée dans le manifest
            val state = packageManager.getComponentEnabledSetting(componentName)
            val isHomeActivityEnabled = state == PackageManager.COMPONENT_ENABLED_STATE_ENABLED

            if (!isHomeActivityEnabled) {
                promise.resolve(false)
                return
            }

            // 2. Vérifier si AliMDM est réellement défini comme launcher par défaut
            val intent = Intent(Intent.ACTION_MAIN)
            intent.addCategory(Intent.CATEGORY_HOME)
            val resolveInfo = packageManager.resolveActivity(intent, PackageManager.MATCH_DEFAULT_ONLY)

            val isDefaultLauncher = resolveInfo?.activityInfo?.packageName == reactApplicationContext.packageName

            DebugLog.d("LauncherModule", "HomeActivity enabled: $isHomeActivityEnabled, Is default launcher: $isDefaultLauncher")
            promise.resolve(isDefaultLauncher)
        } catch (e: Exception) {
            DebugLog.errorProduction("LauncherModule", "Error checking HomeActivity state: ${e.message}")
            promise.reject("ERROR", "Failed to check home launcher state: ${e.message}")
        }
    }

    @ReactMethod
    fun openDefaultLauncherSettings(promise: Promise) {
        try {
            val intent = android.content.Intent(android.provider.Settings.ACTION_HOME_SETTINGS)
            intent.addFlags(android.content.Intent.FLAG_ACTIVITY_NEW_TASK)
            reactApplicationContext.startActivity(intent)
            promise.resolve(true)
        } catch (e: Exception) {
            DebugLog.errorProduction("LauncherModule", "Error opening launcher settings: ${e.message}")
            promise.reject("ERROR", "Failed to open launcher settings: ${e.message}")
        }
    }
}
