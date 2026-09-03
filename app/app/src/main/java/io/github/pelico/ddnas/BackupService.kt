package io.github.pelico.ddnas

import android.app.Notification
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.Uri
import android.os.Build
import android.os.IBinder
import androidx.core.app.NotificationCompat
import androidx.documentfile.provider.DocumentFile
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody
import java.io.InputStream
import java.net.URLEncoder
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

/**
 * 备份前台服务：遍历 SAF 选择的目录树，逐文件上传到中间件
 * /portal/api/openlist/files/upload?path=<远程相对路径>，携带 admin 会话 cookie。
 *
 * 进度通过 [progress] StateFlow 暴露，UI（MainActivity）观察并展示。
 */
class BackupService : Service() {

    private val app: DdnasApplication get() = application as DdnasApplication
    private val client: OkHttpClient get() = app.okHttpClient

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        val treeUri = intent?.getStringExtra(EXTRA_TREE_URI)
        val origin = intent?.getStringExtra(EXTRA_ORIGIN)
        if (treeUri == null || origin == null) {
            stopSelf(); return START_NOT_STICKY
        }
        val cookie = intent.getStringExtra(EXTRA_COOKIE) ?: ""
        val remoteBase = intent.getStringExtra(EXTRA_REMOTE_BASE)
            ?: "/手机备份/${SimpleDateFormat("yyyyMMdd_HHmmss", Locale.getDefault()).format(Date())}"

        startForegroundCompat(NOTIF_ID, buildNotification("准备备份…", 0, 0))
        app.appScope.launch { runBackup(Uri.parse(treeUri), origin, cookie, remoteBase) }
        return START_NOT_STICKY
    }

    private suspend fun runBackup(treeUri: Uri, origin: String, cookie: String, remoteBase: String) {
        try {
            val root = DocumentFile.fromTreeUri(this, treeUri) ?: run {
                _progress.value = Progress.Error("无法访问所选目录"); return
            }
            val files = ArrayList<Pair<DocumentFile, String>>()
            collect(root, "", files)
            val total = files.size
            if (total == 0) { _progress.value = Progress.Done("目录为空，无文件可备份"); return }
            _progress.value = Progress.Running(0, total, files.first().first.name ?: "")
            var done = 0
            var failed = 0
            for ((file, rel) in files) {
                val dest = (remoteBase.trimEnd('/') + "/" + rel).replace(Regex("/+"), "/")
                val ok = try { uploadFile(origin, cookie, dest, file) } catch (e: Exception) { false }
                if (!ok) failed++
                done++
                _progress.value = Progress.Running(done, total, file.name ?: rel)
                startForegroundCompat(NOTIF_ID, buildNotification("备份中", done, total))
            }
            _progress.value = Progress.Done(if (failed == 0) "备份完成：$done 个文件" else "备份完成：成功 ${done - failed} 失败 $failed")
        } catch (e: Exception) {
            _progress.value = Progress.Error(e.message ?: "备份失败")
        } finally {
            stopSelf()
        }
    }

    private fun collect(dir: DocumentFile, prefix: String, out: MutableList<Pair<DocumentFile, String>>) {
        for (child in dir.listFiles()) {
            val name = child.name ?: continue
            val rel = if (prefix.isEmpty()) name else "$prefix/$name"
            if (child.isDirectory) collect(child, rel, out)
            else if (child.isFile) out.add(child to rel)
        }
    }

    private fun uploadFile(origin: String, cookie: String, dest: String, file: DocumentFile): Boolean {
        val url = origin.trimEnd('/') + "/portal/api/openlist/files/upload?path=" + URLEncoder.encode(dest, "UTF-8")
        val length = file.length()
        // 流式 RequestBody：直接读 contentResolver 流，避免整文件入内存。
        val body = object : RequestBody() {
            override fun contentType() = "application/octet-stream".toMediaType()
            override fun contentLength(): Long = length
            override fun writeTo(sink: okio.BufferedSink) {
                contentResolver.openInputStream(file.uri)?.use { input: InputStream ->
                    val buf = ByteArray(64 * 1024)
                    while (true) {
                        val n = input.read(buf)
                        if (n <= 0) break
                        sink.write(buf, 0, n)
                    }
                } ?: throw IllegalStateException("无法读取文件 ${file.name}")
            }
        }
        val req = Request.Builder().url(url).apply {
            if (cookie.isNotEmpty()) header("Cookie", cookie)
        }.put(body).build()
        return client.newCall(req).execute().use { it.isSuccessful }
    }

    private fun buildNotification(text: String, done: Int, total: Int): Notification {
        val builder = NotificationCompat.Builder(this, DdnasApplication.CHANNEL_BACKUP)
            .setContentTitle("DDNAS 备份")
            .setContentText(if (total > 0) "$text ($done/$total)" else text)
            .setSmallIcon(android.R.drawable.stat_sys_upload)
            .setOngoing(true)
        if (total > 0) builder.setProgress(total, done, false)
        return builder.build()
    }

    private fun startForegroundCompat(id: Int, notification: Notification) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) {
            startForeground(id, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC)
        } else {
            startForeground(id, notification)
        }
    }

    sealed interface Progress {
        data object Idle : Progress
        data class Running(val done: Int, val total: Int, val current: String) : Progress
        data class Done(val message: String) : Progress
        data class Error(val message: String) : Progress
    }

    companion object {
        const val EXTRA_TREE_URI = "tree_uri"
        const val EXTRA_ORIGIN = "origin"
        const val EXTRA_COOKIE = "cookie"
        const val EXTRA_REMOTE_BASE = "remote_base"
        private const val NOTIF_ID = 4242
        private val _progress = MutableStateFlow<Progress>(Progress.Idle)
        val progress: StateFlow<Progress> = _progress
        fun reset() { _progress.value = Progress.Idle }
    }
}
