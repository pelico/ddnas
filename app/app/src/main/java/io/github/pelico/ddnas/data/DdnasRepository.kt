package io.github.pelico.ddnas.data

import io.github.pelico.ddnas.data.model.AdaptersResponse
import io.github.pelico.ddnas.data.model.FileListResponse
import io.github.pelico.ddnas.data.model.HealthResponse
import io.github.pelico.ddnas.data.model.SystemInfo
import io.github.pelico.ddnas.net.RetrofitProvider
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import okhttp3.MediaType.Companion.toMediaTypeOrNull
import okhttp3.Request
import okhttp3.RequestBody
import okio.BufferedSink
import java.io.InputStream
import java.net.URLEncoder

/**
 * High-level gateway to the middleware. Builds absolute URLs from the user
 * configured base address and delegates to [DdnasApi] (GET endpoints) and to
 * the shared [RetrofitProvider.okHttpClient] for the streaming upload.
 */
class DdnasRepository {

    private val api = RetrofitProvider.api
    private val http = RetrofitProvider.okHttpClient

    private fun url(base: String, path: String): String {
        val b = base.trimEnd('/')
        val p = if (path.startsWith('/')) path else "/$path"
        return b + p
    }

    suspend fun health(base: String): HealthResponse =
        api.health(url(base, "/api/health"))

    suspend fun adapters(base: String): AdaptersResponse =
        api.adapters(url(base, "/api/adapters"))

    suspend fun systemInfo(base: String): SystemInfo =
        api.systemInfo(url(base, "/api/node/system"))

    suspend fun listFiles(base: String, path: String): FileListResponse =
        api.listFiles(url(base, "/api/openlist/files/list"), path)

    /**
     * Builds the absolute URL of a media stream for [path] (a path that starts
     * with "/"), suitable as an ExoPlayer MediaItem URL. The Bearer header is
     * added via the OkHttp data source factory, not in the URL.
     */
    fun streamUrl(base: String, path: String): String {
        val p = if (path.startsWith('/')) path.substring(1) else path
        return url(base, "/api/openlist/files/stream/$p")
    }

    /**
     * Streams [content] (opened lazily on the IO dispatcher) to the middleware
     * upload endpoint, without buffering the whole file into memory.
     *
     * @param base middleware base URL
     * @param destPath full destination path including file name, e.g. "/movies/a.mp4"
     * @param content opens a fresh stream each time it is called
     * @param length file size in bytes, or -1 if unknown (uses chunked encoding)
     */
    suspend fun uploadFile(
        base: String,
        destPath: String,
        content: () -> InputStream,
        length: Long
    ): Boolean = withContext(Dispatchers.IO) {
        val encoded = URLEncoder.encode(destPath, "UTF-8")
        val requestUrl = url(base, "/api/openlist/files/upload") + "?path=$encoded"
        val body = InputStreamRequestBody(content, length, "application/octet-stream")
        val request = Request.Builder()
            .url(requestUrl)
            .post(body)
            .build()
        http.newCall(request).execute().use { response -> response.isSuccessful }
    }
}

/**
 * [RequestBody] that pulls bytes from an [InputStream] in [writeTo], so large
 * uploads never need to fit in memory. The stream is opened once when OkHttp
 * starts writing and closed when it finishes.
 */
private class InputStreamRequestBody(
    private val openStream: () -> InputStream,
    private val contentLength: Long,
    contentType: String
) : RequestBody() {

    private val mediaType = contentType.toMediaTypeOrNull()

    override fun contentType() = mediaType

    override fun contentLength(): Long = contentLength

    override fun writeTo(sink: BufferedSink) {
        openStream().use { input ->
            val buffer = ByteArray(DEFAULT_BUFFER)
            while (true) {
                val read = input.read(buffer)
                if (read == -1) break
                sink.write(buffer, 0, read)
            }
        }
    }

    private companion object {
        private const val DEFAULT_BUFFER = 8 * 1024
    }
}
