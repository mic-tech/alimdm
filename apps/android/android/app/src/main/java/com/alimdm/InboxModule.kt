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
    fun saveToInbox(
        sourcePath: String,
        displayName: String,
        mimeType: String,
        relDir: String?,
        promise: Promise,
    ) {
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
        // Sanitised again here rather than trusted from the server: this is what
        // actually builds a filesystem path, and safeName() exists for the same
        // reason one segment down.
        val dir = safeRelDir(relDir)
        try {
            val path = if (useMediaStore) {
                saveViaMediaStore(src, name, mimeType, dir)
            } else {
                saveViaLegacyFile(src, name, dir)
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

    private fun saveViaMediaStore(src: File, name: String, mimeType: String, relDir: String): String {
        val resolver = reactContext.contentResolver
        // MediaStore creates whatever folders RELATIVE_PATH names, so a nested
        // upload needs no mkdir of its own here.
        val relative = if (relDir.isEmpty()) DOWNLOADS_RELATIVE
                       else "$DOWNLOADS_RELATIVE${File.separator}$relDir"
        // Replace rather than accumulate: MediaStore would otherwise silently
        // write "worksheet (1).pdf" every time a file is re-pushed after a fix.
        deleteExisting(name, relative)

        val values = ContentValues().apply {
            put(MediaStore.Downloads.DISPLAY_NAME, name)
            if (mimeType.isNotBlank()) put(MediaStore.Downloads.MIME_TYPE, mimeType)
            put(MediaStore.Downloads.RELATIVE_PATH, relative)
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

        return "$relative/$name"
    }

    private fun saveViaLegacyFile(src: File, name: String, relDir: String): String {
        var dir = File(
            Environment.getExternalStoragePublicDirectory(Environment.DIRECTORY_DOWNLOADS),
            INBOX_FOLDER
        )
        if (relDir.isNotEmpty()) dir = File(dir, relDir)
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
                    MediaStore.Downloads.DATE_MODIFIED,
                    MediaStore.Downloads.RELATIVE_PATH
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
                    val relCol = c.getColumnIndexOrThrow(MediaStore.Downloads.RELATIVE_PATH)
                    while (c.moveToNext()) {
                        out.pushMap(Arguments.createMap().apply {
                            putString("name", c.getString(nameCol) ?: "")
                            putDouble("size", c.getLong(sizeCol).toDouble())
                            putString("mime_type", c.getString(mimeCol) ?: "")
                            // MediaStore keeps this in seconds; JS expects millis.
                            putDouble("modified_at", c.getLong(dateCol) * 1000.0)
                            // Which folder inside the inbox. The LIKE above already
                            // matched subfolders, so without this two tracks named
                            // the same in different surahs are indistinguishable —
                            // and a delete could not tell them apart.
                            putString("rel_path", relDirOf(c.getString(relCol) ?: ""))
                        })
                    }
                }
            } else {
                val dir = File(
                    Environment.getExternalStoragePublicDirectory(Environment.DIRECTORY_DOWNLOADS),
                    INBOX_FOLDER
                )
                // walkTopDown so a folder upload is not invisible here; the
                // MediaStore branch above already matched subfolders.
                dir.walkTopDown().filter { it.isFile }
                    .sortedByDescending { it.lastModified() }
                    .forEach { f ->
                        val rel = f.parentFile?.relativeToOrNull(dir)?.path.orEmpty()
                        out.pushMap(Arguments.createMap().apply {
                            putString("name", f.name)
                            putDouble("size", f.length().toDouble())
                            putString("mime_type", "")
                            putDouble("modified_at", f.lastModified().toDouble())
                            putString("rel_path", if (rel == ".") "" else rel)
                        })
                    }
            }
            promise.resolve(out)
        } catch (e: Exception) {
            Log.e(TAG, "Could not list the inbox", e)
            promise.reject("LIST_FAILED", e.message ?: "Could not list the inbox", e)
        }
    }

    @ReactMethod
    fun deleteFromInbox(displayName: String, relDir: String?, promise: Promise) {
        val name = safeName(displayName)
        if (name == null) {
            promise.reject("BAD_NAME", "Unusable file name: $displayName")
            return
        }
        val dir = safeRelDir(relDir)
        try {
            val removed = if (useMediaStore) {
                val relative = if (dir.isEmpty()) DOWNLOADS_RELATIVE
                               else "$DOWNLOADS_RELATIVE${File.separator}$dir"
                deleteExisting(name, relative) > 0
            } else {
                var base = File(
                    Environment.getExternalStoragePublicDirectory(Environment.DIRECTORY_DOWNLOADS),
                    INBOX_FOLDER
                )
                if (dir.isNotEmpty()) base = File(base, dir)
                File(base, name).delete()
            }
            promise.resolve(removed)
        } catch (e: Exception) {
            Log.e(TAG, "Could not delete $name", e)
            promise.reject("DELETE_FAILED", e.message ?: "Could not delete the file", e)
        }
    }

    /**
     * Removes any existing inbox entry with this name in this folder.
     *
     * Scoped to the exact folder, not a LIKE over the whole inbox: once uploads
     * can nest, "track01.mp3" exists in as many folders as there are surahs,
     * and a prefix match would delete every one of them to write a single file.
     */
    private fun deleteExisting(name: String, relative: String): Int {
        if (!useMediaStore) return 0
        val collection = MediaStore.Downloads.getContentUri(MediaStore.VOLUME_EXTERNAL_PRIMARY)
        // MediaStore stores RELATIVE_PATH with a trailing separator.
        val exact = if (relative.endsWith(File.separator)) relative else relative + File.separator
        return try {
            reactContext.contentResolver.delete(
                collection,
                "${MediaStore.Downloads.RELATIVE_PATH} = ? AND ${MediaStore.Downloads.DISPLAY_NAME} = ?",
                arrayOf(exact, name)
            )
        } catch (e: Exception) {
            Log.w(TAG, "Could not clear an existing $name: ${e.message}")
            0
        }
    }

    /**
     * Turn a MediaStore RELATIVE_PATH ("Download/Ali MDM/Juz30/Surah-078/") into
     * the folder within the inbox ("Juz30/Surah-078"), or "" at the top.
     */
    private fun relDirOf(relativePath: String): String {
        val trimmed = relativePath.trim('/')
        val prefix = DOWNLOADS_RELATIVE.trim('/')
        if (!trimmed.startsWith(prefix)) return ""
        return trimmed.removePrefix(prefix).trim('/')
    }

    /**
     * Reduce a server-supplied folder path to safe segments, or "" for the top
     * of the inbox. Mirrors blob.SanitizeRelPath on the server; both run because
     * this is the one that turns into a real path on a real filesystem.
     */
    private fun safeRelDir(raw: String?): String {
        if (raw.isNullOrBlank()) return ""
        return raw.replace('\\', '/')
            .split('/')
            .mapNotNull { safeName(it) }
            .take(8)
            .joinToString(File.separator)
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
