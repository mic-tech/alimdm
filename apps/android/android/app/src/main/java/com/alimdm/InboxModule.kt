package com.alimdm

import android.content.ContentValues
import android.content.Context
import android.net.Uri
import android.os.Build
import android.os.Environment
import android.provider.MediaStore
import android.util.Log
import com.facebook.react.bridge.*
import java.io.File

/**
 * Writes files pushed from the console into a folder on the tablet that the
 * pupil's own Files app can open.
 *
 * The destination is the shared Downloads collection, under an "Ali MDM"
 * subfolder, rather than the app's private storage. A worksheet nobody can open
 * outside the kiosk is not a delivered worksheet: the whole point is that a
 * pupil taps it and a PDF viewer opens.
 *
 * Two storage worlds, because minSdk is 24:
 *  - API 29+ (every tablet in the fleet): MediaStore. Scoped storage forbids
 *    writing directly into a folder other apps also use.
 *  - API 24-28: the plain public Downloads directory.
 */
class InboxModule(private val reactContext: ReactApplicationContext) :
    ReactContextBaseJavaModule(reactContext) {

    override fun getName() = "InboxModule"

    companion object {
        private const val TAG = "InboxModule"

        /** Shown to the pupil as the folder name, so it says who put it there. */
        const val INBOX_FOLDER = "Ali MDM"

        private val DOWNLOADS_RELATIVE = Environment.DIRECTORY_DOWNLOADS + File.separator + INBOX_FOLDER
    }

    private val useMediaStore: Boolean
        get() = Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q

    /**
     * Copy a already-downloaded file into the inbox.
     *
     * Takes a path rather than the bytes: a slide deck as a base64 string would
     * be held in JS memory, in the bridge, and again as a ByteArray. The file is
     * streamed instead.
     */
    @ReactMethod
    fun saveToInbox(sourcePath: String, displayName: String, mimeType: String, promise: Promise) {
        val src = File(sourcePath)
        if (!src.exists()) {
            promise.reject("NO_SOURCE", "Downloaded file is missing: $sourcePath")
            return
        }
        val name = safeName(displayName)
        if (name == null) {
            promise.reject("BAD_NAME", "Unusable file name: $displayName")
            return
        }
        try {
            val path = if (useMediaStore) {
                saveViaMediaStore(src, name, mimeType)
            } else {
                saveViaLegacyFile(src, name)
            }
            promise.resolve(Arguments.createMap().apply {
                putString("name", name)
                putString("path", path)
                putDouble("size", src.length().toDouble())
            })
        } catch (e: Exception) {
            Log.e(TAG, "Could not save $name to the inbox", e)
            promise.reject("SAVE_FAILED", e.message ?: "Could not save the file", e)
        }
    }

    private fun saveViaMediaStore(src: File, name: String, mimeType: String): String {
        val resolver = reactContext.contentResolver
        // Replace rather than accumulate: MediaStore would otherwise silently
        // write "worksheet (1).pdf" every time a file is re-pushed after a fix.
        deleteExisting(name)

        val values = ContentValues().apply {
            put(MediaStore.Downloads.DISPLAY_NAME, name)
            if (mimeType.isNotBlank()) put(MediaStore.Downloads.MIME_TYPE, mimeType)
            put(MediaStore.Downloads.RELATIVE_PATH, DOWNLOADS_RELATIVE)
            // Hides the entry until the bytes are all there, so a viewer cannot
            // open a half-written PDF.
            put(MediaStore.Downloads.IS_PENDING, 1)
        }
        val collection = MediaStore.Downloads.getContentUri(MediaStore.VOLUME_EXTERNAL_PRIMARY)
        val uri: Uri = resolver.insert(collection, values)
            ?: throw IllegalStateException("MediaStore refused to create the entry")

        try {
            resolver.openOutputStream(uri)?.use { out ->
                src.inputStream().use { input -> input.copyTo(out) }
            } ?: throw IllegalStateException("MediaStore returned no output stream")
        } catch (e: Exception) {
            // Leaving a pending row behind would be invisible to the pupil and
            // would still occupy the name.
            resolver.delete(uri, null, null)
            throw e
        }

        resolver.update(uri, ContentValues().apply {
            put(MediaStore.Downloads.IS_PENDING, 0)
        }, null, null)

        return "$DOWNLOADS_RELATIVE/$name"
    }

    private fun saveViaLegacyFile(src: File, name: String): String {
        val dir = File(
            Environment.getExternalStoragePublicDirectory(Environment.DIRECTORY_DOWNLOADS),
            INBOX_FOLDER
        )
        if (!dir.exists() && !dir.mkdirs()) {
            throw IllegalStateException("Could not create ${dir.absolutePath}")
        }
        val dest = File(dir, name)
        src.inputStream().use { input ->
            dest.outputStream().use { out -> input.copyTo(out) }
        }
        return dest.absolutePath
    }

    /** List what is currently in the inbox. Backs the console's file manager. */
    @ReactMethod
    fun listInbox(promise: Promise) {
        try {
            val out = Arguments.createArray()
            if (useMediaStore) {
                val collection = MediaStore.Downloads.getContentUri(MediaStore.VOLUME_EXTERNAL_PRIMARY)
                val projection = arrayOf(
                    MediaStore.Downloads._ID,
                    MediaStore.Downloads.DISPLAY_NAME,
                    MediaStore.Downloads.SIZE,
                    MediaStore.Downloads.MIME_TYPE,
                    MediaStore.Downloads.DATE_MODIFIED
                )
                // RELATIVE_PATH comparison needs the trailing separator MediaStore stores.
                val selection = "${MediaStore.Downloads.RELATIVE_PATH} LIKE ?"
                val args = arrayOf("$DOWNLOADS_RELATIVE%")
                reactContext.contentResolver.query(
                    collection, projection, selection, args,
                    "${MediaStore.Downloads.DATE_MODIFIED} DESC"
                )?.use { c ->
                    val nameCol = c.getColumnIndexOrThrow(MediaStore.Downloads.DISPLAY_NAME)
                    val sizeCol = c.getColumnIndexOrThrow(MediaStore.Downloads.SIZE)
                    val mimeCol = c.getColumnIndexOrThrow(MediaStore.Downloads.MIME_TYPE)
                    val dateCol = c.getColumnIndexOrThrow(MediaStore.Downloads.DATE_MODIFIED)
                    while (c.moveToNext()) {
                        out.pushMap(Arguments.createMap().apply {
                            putString("name", c.getString(nameCol) ?: "")
                            putDouble("size", c.getLong(sizeCol).toDouble())
                            putString("mime_type", c.getString(mimeCol) ?: "")
                            // MediaStore keeps this in seconds; JS expects millis.
                            putDouble("modified_at", c.getLong(dateCol) * 1000.0)
                        })
                    }
                }
            } else {
                val dir = File(
                    Environment.getExternalStoragePublicDirectory(Environment.DIRECTORY_DOWNLOADS),
                    INBOX_FOLDER
                )
                dir.listFiles()?.sortedByDescending { it.lastModified() }?.forEach { f ->
                    if (f.isFile) {
                        out.pushMap(Arguments.createMap().apply {
                            putString("name", f.name)
                            putDouble("size", f.length().toDouble())
                            putString("mime_type", "")
                            putDouble("modified_at", f.lastModified().toDouble())
                        })
                    }
                }
            }
            promise.resolve(out)
        } catch (e: Exception) {
            Log.e(TAG, "Could not list the inbox", e)
            promise.reject("LIST_FAILED", e.message ?: "Could not list the inbox", e)
        }
    }

    @ReactMethod
    fun deleteFromInbox(displayName: String, promise: Promise) {
        val name = safeName(displayName)
        if (name == null) {
            promise.reject("BAD_NAME", "Unusable file name: $displayName")
            return
        }
        try {
            val removed = if (useMediaStore) {
                deleteExisting(name) > 0
            } else {
                File(
                    File(
                        Environment.getExternalStoragePublicDirectory(Environment.DIRECTORY_DOWNLOADS),
                        INBOX_FOLDER
                    ), name
                ).delete()
            }
            promise.resolve(removed)
        } catch (e: Exception) {
            Log.e(TAG, "Could not delete $name", e)
            promise.reject("DELETE_FAILED", e.message ?: "Could not delete the file", e)
        }
    }

    /** Removes any existing inbox entry with this name. Returns rows deleted. */
    private fun deleteExisting(name: String): Int {
        if (!useMediaStore) return 0
        val collection = MediaStore.Downloads.getContentUri(MediaStore.VOLUME_EXTERNAL_PRIMARY)
        return try {
            reactContext.contentResolver.delete(
                collection,
                "${MediaStore.Downloads.RELATIVE_PATH} LIKE ? AND ${MediaStore.Downloads.DISPLAY_NAME} = ?",
                arrayOf("$DOWNLOADS_RELATIVE%", name)
            )
        } catch (e: Exception) {
            Log.w(TAG, "Could not clear an existing $name: ${e.message}")
            0
        }
    }

    /**
     * The server sanitises names too, but this runs on whatever actually
     * arrives: a separator here would place the file outside the inbox folder.
     */
    private fun safeName(raw: String): String? {
        val name = raw.trim()
            .replace('/', '_')
            .replace('\\', '_')
            .trimStart('.')
        if (name.isEmpty() || name == "." || name == "..") return null
        return if (name.length > 120) name.takeLast(120) else name
    }
}
