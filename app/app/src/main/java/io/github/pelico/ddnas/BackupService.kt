package io.github.pelico.ddnas

import android.app.Notification
import android.app.Service
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.Uri
import android.os.Build
import android.os.IBinder
import androidx.core.app.NotificationCompat
import androidx.documentfile.provider.DocumentFile
import io.github.pelico.ddnas.data.BackupManifest
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

/**
 * 备份前台服务：遍历 SAF 选择的目录树，增量上传到中间件
 * /portal/api/files/upload?path=<远程相对路径>，携带 admin 会话 cookie。
 *
 * 增量策略：BackupManifest 记录每个文件上次上传的 size+mtime，
 * 未变更的文件跳过，只传新增/修改的文件。
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
            _progress.value = Progress.Error("缺少备份参数"); stopSelf(); return START_NOT_STICKY
        }
        // 防止重复启动：若已有备份在跑，拒绝新任务。
        // 正常流程下前端按钮在运行期间会切换为"取消备份"，此处仅作并发兜底。
        if (running) return START_NOT_STICKY
        running = true
        cancelled = false
        val cookie = intent.getStringExtra(EXTRA_COOKIE) ?: ""
        val remoteBase = intent.getStringExtra(EXTRA_REMOTE_BASE) ?: "/手机备份"

        startForegroundCompat(NOTIF_ID, buildNotification("准备备份…", 0, 0))
        app.appScope.launch { runBackup(Uri.parse(treeUri), origin, cookie, remoteBase) }
        return START_NOT_STICKY
    }

    private suspend fun runBackup(treeUri: Uri, origin: String, cookie: String, remoteBase: String) {
        val manifest = BackupManifest(this, treeUri.toString())
        val startTime = System.currentTimeMillis()
        val failedFiles = mutableListOf<String>()
        try {
            // 持久化 SAF 权限，避免重启后失效
            try {
                contentResolver.takePersistableUriPermission(
                    treeUri,
                    Intent.FLAG_GRANT_READ_URI_PERMISSION
                )
            } catch (_: Exception) { }

            val root = DocumentFile.fromTreeUri(this, treeUri) ?: run {
                _progress.value = Progress.Error("无法访问所选目录"); return
            }
            if (!root.isDirectory) {
                _progress.value = Progress.Error("所选路径不是目录"); return
            }

            _progress.value = Progress.Scanning
            val all = ArrayList<Pair<DocumentFile, String>>()
            collect(root, "", all)

            // 增量过滤：只保留需要上传的文件
            val toUpload = ArrayList<Pair<DocumentFile, String>>()
            var skipped = 0
            for ((file, rel) in all) {
                val size = file.length()
                val mtime = file.lastModified()
                if (manifest.needUpload(rel, size, mtime)) {
                    toUpload.add(file to rel)
                } else {
                    skipped++
                }
            }

            val total = toUpload.size
            if (total == 0) {
                _progress.value = Progress.Done("无需备份（$skipped 个文件未变更）")
                return
            }

            // 备份前确保远程根目录存在且可写，避免所有文件静默失败
            val mErr = mkdirRemote(origin, cookie, remoteBase)
            if (mErr != null) {
                _progress.value = Progress.Error(mErr)
                return
            }

            _progress.value = Progress.Running(0, total, toUpload.first().first.name ?: "")
            var done = 0
            var failed = 0
            for ((file, rel) in toUpload) {
                // 用户点"取消备份"后，等当前文件传完即退出循环
                if (cancelled) {
                    _progress.value = Progress.Done("已取消（已传 $done 个文件）")
                    return
                }
                val dest = (remoteBase.trimEnd('/') + "/" + rel).replace(Regex("/+"), "/")
                // 单文件最多重试 3 次，指数退避：1s → 2s → 4s
                var ok = false
                for (attempt in 0..2) {
                    if (cancelled) break
                    ok = try {
                        uploadFile(origin, cookie, dest, file)
                    } catch (e: Exception) {
                        Log_w("upload attempt ${attempt + 1} fail: $rel", e); false
                    }
                    if (ok) break
                    if (attempt < 2) {
                        val backoff = (1000L shl attempt) // 1s, 2s
                        _progress.value = Progress.Running(done, total, "重试(${attempt + 1}/3) ${file.name ?: rel}")
                        try { kotlinx.coroutines.delay(backoff) } catch (_: Exception) { break }
                    }
                }
                if (ok) {
                    manifest.markUploaded(rel, file.length(), file.lastModified())
                } else {
                    failed++
                    failedFiles.add(rel)
                }
                done++
                _progress.value = Progress.Running(done, total, file.name ?: rel)
                startForegroundCompat(NOTIF_ID, buildNotification("备份中", done, total))
            }
            val msg = buildString {
                append("备份完成：上传 $done 个文件")
                if (skipped > 0) append("，跳过 $skipped 个未变更")
                if (failed > 0) append("，失败 $failed 个")
            }
            // 上报备份历史到中间件 SQLite
            reportHistory(origin, cookie, startTime, total, done - failed, failed, failedFiles, treeUri.toString(), remoteBase)
            // 全部失败时报 Error，避免用户误以为备份成功
            if (failed == total) {
                _progress.value = Progress.Error("全部 $failed 个文件上传失败（请检查 OpenList 挂载与写入权限）")
            } else {
                _progress.value = Progress.Done(msg)
            }
        } catch (e: Exception) {
            _progress.value = Progress.Error(e.message ?: "备份失败")
        } finally {
            running = false
            cancelled = false
            stopSelf()
        }
    }

    /**
     * 供 [BackupWorker] 调用的入口，复用 [runBackup] 的核心逻辑。
     * 不涉及 Service 生命周期（无 stopSelf / startForeground），
     * 进度通过 [progress] StateFlow 暴露给 UI。
     */
    suspend fun runBackupForWorker(treeUri: Uri, origin: String, cookie: String, remoteBase: String) {
        running = true
        cancelled = false
        try {
            val manifest = BackupManifest(this, treeUri.toString())
            val root = DocumentFile.fromTreeUri(this, treeUri) ?: return
            if (!root.isDirectory) return
            _progress.value = Progress.Scanning
            val all = ArrayList<Pair<DocumentFile, String>>()
            collect(root, "", all)
            val toUpload = ArrayList<Pair<DocumentFile, String>>()
            var skipped = 0
            for ((file, rel) in all) {
                if (!manifest.needUpload(rel, file.length(), file.lastModified())) { skipped++; continue }
                toUpload.add(file to rel)
            }
            val total = toUpload.size
            if (total == 0) { _progress.value = Progress.Done("无需备份"); return }
            val mErr = mkdirRemote(origin, cookie, remoteBase)
            if (mErr != null) { _progress.value = Progress.Error(mErr); return }
            _progress.value = Progress.Running(0, total, toUpload.first().first.name ?: "")
            var done = 0; var failed = 0
            for ((file, rel) in toUpload) {
                if (cancelled) break
                val dest = (remoteBase.trimEnd('/') + "/" + rel).replace(Regex("/+"), "/")
                var ok = false
                for (attempt in 0..2) {
                    if (cancelled) break
                    ok = try { uploadFile(origin, cookie, dest, file) } catch (e: Exception) { Log_w("worker upload fail: $rel", e); false }
                    if (ok) break
                    if (attempt < 2) try { kotlinx.coroutines.delay(1000L shl attempt) } catch (_: Exception) { break }
                }
                if (ok) manifest.markUploaded(rel, file.length(), file.lastModified()) else failed++
                done++
                _progress.value = Progress.Running(done, total, file.name ?: rel)
            }
            if (failed == total) { _progress.value = Progress.Error("全部 $failed 个文件上传失败") }
            else { _progress.value = Progress.Done("备份完成：上传 $done 个${if (failed > 0) "，失败 $failed 个" else ""}") }
        } catch (e: Exception) {
            _progress.value = Progress.Error(e.message ?: "备份失败")
        } finally {
            running = false
            cancelled = false
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

    /** 备份完成后上报历史到中间件 SQLite，供 portal 查看历史与失败文件列表。 */
    private fun reportHistory(
        origin: String, cookie: String, startTime: Long,
        total: Int, success: Int, failed: Int, failedFiles: List<String>,
        treeUri: String, remoteBase: String
    ) {
        try {
            val duration = System.currentTimeMillis() - startTime
            // 构造 JSON：{"ts":now,"duration_ms":dur,"total":N,"success":N,"failed":N,"failed_list":["a","b"],"tree_hash":"...","remote_base":"/.../"}
            val fl = failedFiles.joinToString(",") { "\"${it.replace("\"", "\\\"")}\"" }
            val treeHash = treeUri.hashCode().toString(16)
            val json = """{"ts":${System.currentTimeMillis()},"duration_ms":$duration,"total":$total,"success":$success,"failed":$failed,"failed_list":[$fl],"tree_hash":"$treeHash","remote_base":"$remoteBase"}"""
            val req = Request.Builder()
                .url(origin.trimEnd('/') + "/portal/api/backup/history")
                .apply { if (cookie.isNotEmpty()) header("Cookie", cookie) }
                .post(RequestBody.create("application/json; charset=utf-8".toMediaType(), json))
                .build()
            client.newCall(req).execute().use { it.body?.string() }
        } catch (e: Exception) {
            Log_w("reportHistory fail", e)
        }
    }

    /** 调用中间件 mkdir 接口确保远程目录存在且可写。
     *  返回 null 表示可用；返回非空字符串表示失败原因（带 HTTP code/上游错误，便于排查）。 */
    private fun mkdirRemote(origin: String, cookie: String, remoteBase: String): String? {
        val base = remoteBase.trimEnd('/')
        if (base.isEmpty()) return null  // 根目录跳过
        val url = origin.trimEnd('/') + "/portal/api/files/mkdir?path=" + URLEncoder.encode(base, "UTF-8")
        val req = Request.Builder().url(url).apply {
            if (cookie.isNotEmpty()) header("Cookie", cookie)
        }.post(EMPTY_BODY).build()
        return try {
            client.newCall(req).execute().use { resp ->
                val body = resp.body?.string() ?: ""
                if (!resp.isSuccessful) {
                    // 401 = cookie 失效；其他 = 上游/路径问题
                    val hint = if (resp.code == 401) "登录已失效，请重新打开页面后再备份"
                               else "HTTP ${resp.code}"
                    Log_w("mkdir fail: $base HTTP ${resp.code} body=${body.take(120)}", Exception("mkdir HTTP ${resp.code}"))
                    "远程目录不可用：$base（$hint）"
                } else if (!body.contains("\"ok\":true") && !body.contains("\"ok\": true")) {
                    // 业务失败：上游返回 ok:false 或错误 JSON，尝试提取 error/message
                    val msg = Regex("\"(?:error|message)\"\\s*:\\s*\"([^\"]+)\"").find(body)?.groupValues?.get(1)
                        ?: body.take(80)
                    Log_w("mkdir biz fail: $base body=${body.take(120)}", Exception("mkdir biz fail"))
                    "远程目录不可用：$base（$msg）"
                } else {
                    null
                }
            }
        } catch (e: Exception) {
            Log_w("mkdir exception: $base", e)
            "远程目录不可用：$base（网络异常：${e.message}）"
        }
    }

    private fun uploadFile(origin: String, cookie: String, dest: String, file: DocumentFile): Boolean {
        val url = origin.trimEnd('/') + "/portal/api/files/upload?path=" + URLEncoder.encode(dest, "UTF-8")
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

    // Log_w 包装，避免 import android.util.Log（保持文件简洁）
    private fun Log_w(msg: String, e: Exception) {
        android.util.Log.w("DDNAS-Backup", msg, e)
    }

    sealed interface Progress {
        data object Idle : Progress
        data object Scanning : Progress
        data class Running(val done: Int, val total: Int, val current: String) : Progress
        data class Done(val message: String) : Progress
        data class Error(val message: String) : Progress

        /** JSON 字符串转义：处理 \ " 和换行。
         *  必须放在 sealed interface 内，否则成员 toJson() 无权访问 BackupService 类的私有方法。 */
        private fun jsonStr(s: String): String =
            "\"" + s.replace("\\", "\\\\").replace("\"", "\\\"").replace("\n", "\\n") + "\""

        /** 序列化为 portal 端 __onBackupProgress(p) 可解析的 JSON 字符串。 */
        fun toJson(): String = when (this) {
            is Idle -> """{"phase":"idle"}"""
            is Scanning -> """{"phase":"scanning"}"""
            is Running -> """{"phase":"running","done":$done,"total":$total,"current":""" + jsonStr(current) + """}"""
            is Done -> """{"phase":"done","message":""" + jsonStr(message) + """}"""
            is Error -> """{"phase":"error","message":""" + jsonStr(message) + """}"""
        }
    }

    companion object {
        const val EXTRA_TREE_URI = "tree_uri"
        const val EXTRA_ORIGIN = "origin"
        const val EXTRA_COOKIE = "cookie"
        const val EXTRA_REMOTE_BASE = "remote_base"
        private const val NOTIF_ID = 4242
        // mkdir 用的空 RequestBody（OkHttp POST 需要非空 body）
        private val EMPTY_BODY = RequestBody.create(null, ByteArray(0))
        private val _progress = MutableStateFlow<Progress>(Progress.Idle)
        val progress: StateFlow<Progress> = _progress

        // 运行中标志：onStartCommand 拒绝重复启动，startBackupService 前置检查
        @Volatile private var running = false
        // 取消标志：用户点"取消备份"后置 true，runBackup 循环检测后退出
        @Volatile private var cancelled = false
        fun isRunning(): Boolean = running
        fun cancel() { cancelled = true }
    }
}
