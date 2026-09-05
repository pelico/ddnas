package io.github.pelico.ddnas

import android.content.Context
import android.content.Intent
import android.net.Uri
import androidx.documentfile.provider.DocumentFile
import io.github.pelico.ddnas.data.BackupManifest
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody
import java.io.InputStream
import java.net.URLEncoder

/**
 * 备份上传核心逻辑，与 Service/Worker 生命周期解耦。
 *
 * 历史 Bug：BackupWorker 直接 `val service = BackupService()` 实例化 Service 组件，
 * 由于 Service 未经系统 startService 挂载基础 Context，其 mBase 为 null，
 * 调用 getSharedPreferences / contentResolver 时抛 NPE
 * （"Attempt to invoke virtual method 'android.content.SharedPreferences ...'"）。
 *
 * 修复：把上传逻辑抽到本类，构造时注入合法 Context。
 *  - Service 由系统启动，this 即为合法 Context
 *  - Worker 用 applicationContext
 * 两者都通过 BackupEngine 执行备份，不再直接 new Service。
 *
 * 进度通过 [BackupService.emitProgress] 写入共享 StateFlow，UI 照常观察。
 * 取消标志读 [BackupService.isCancelled]，与"立即备份"按钮共享同一取消通道。
 */
class BackupEngine(
    private val context: Context,
    private val client: OkHttpClient
) {
    /** 备份结果：供 Worker 判断是否写入 lastBackupTime 与返回 success/retry。 */
    sealed class Result {
        data object Success : Result()        // 上传完成（含"无需备份"）
        data object Cancelled : Result()      // 用户取消
        data class Failed(val message: String) : Result() // 出错/全部失败
    }

    /**
     * 执行增量备份。
     * @param reportHistory 是否上报历史到中间件 SQLite（手动备份上报；Worker 也上报，便于 portal 查看）
     */
    suspend fun runBackup(
        treeUri: Uri, origin: String, cookie: String, remoteBase: String,
        reportHistory: Boolean = true
    ): Result {
        val manifest = BackupManifest(context, treeUri.toString(), remoteBase)
        val startTime = System.currentTimeMillis()
        val failedFiles = mutableListOf<String>()
        try {
            // 持久化 SAF 权限，避免重启后失效
            try {
                context.contentResolver.takePersistableUriPermission(
                    treeUri,
                    Intent.FLAG_GRANT_READ_URI_PERMISSION
                )
            } catch (_: Exception) { }

            val root = DocumentFile.fromTreeUri(context, treeUri) ?: run {
                emit(BackupService.Progress.Error("无法访问所选目录"))
                return Result.Failed("无法访问所选目录")
            }
            if (!root.isDirectory) {
                emit(BackupService.Progress.Error("所选路径不是目录"))
                return Result.Failed("所选路径不是目录")
            }

            emit(BackupService.Progress.Scanning)
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
                emit(BackupService.Progress.Done("无需备份（$skipped 个文件未变更）"))
                return Result.Success
            }

            // 备份前确保远程根目录存在且可写，避免所有文件静默失败
            val mErr = mkdirRemote(origin, cookie, remoteBase)
            if (mErr != null) {
                emit(BackupService.Progress.Error(mErr))
                return Result.Failed(mErr)
            }

            emit(BackupService.Progress.Running(0, total, toUpload.first().first.name ?: ""))
            var done = 0
            var failed = 0
            // 已创建的远程子目录缓存：根目录已 mkdir 过，加入避免重复
            val mkdirCache = mutableSetOf(remoteBase.trimEnd('/'))
            for ((file, rel) in toUpload) {
                // 用户点"取消备份"后，等当前文件传完即退出循环
                if (BackupService.isCancelled()) {
                    emit(BackupService.Progress.Done("已取消（已传 $done 个文件）"))
                    return Result.Cancelled
                }
                val dest = (remoteBase.trimEnd('/') + "/" + rel).replace(Regex("/+"), "/")
                val parent = dest.substringBeforeLast('/').trimEnd('/')
                if (parent.isNotEmpty() && parent != remoteBase.trimEnd('/')) {
                    val pErr = ensureRemoteDirs(origin, cookie, remoteBase, parent, mkdirCache)
                    if (pErr != null) {
                        failed++; failedFiles.add(rel); done++
                        emit(BackupService.Progress.Running(done, total, "目录创建失败: $pErr"))
                        continue
                    }
                }
                // 单文件最多重试 3 次，指数退避：1s → 2s → 4s
                var ok = false
                for (attempt in 0..2) {
                    if (BackupService.isCancelled()) break
                    ok = try {
                        uploadFile(origin, cookie, dest, file, done, total)
                    } catch (e: Exception) {
                        Log_w("upload attempt ${attempt + 1} fail: $rel", e); false
                    }
                    if (ok) break
                    if (attempt < 2) {
                        val backoff = (1000L shl attempt)
                        emit(BackupService.Progress.Running(done, total, "重试(${attempt + 1}/3) ${file.name ?: rel}"))
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
                emit(BackupService.Progress.Running(done, total, file.name ?: rel))
            }
            val msg = buildString {
                append("备份完成：上传 $done 个文件")
                if (skipped > 0) append("，跳过 $skipped 个未变更")
                if (failed > 0) append("，失败 $failed 个")
            }
            // 上报备份历史到中间件 SQLite
            if (reportHistory) {
                reportHistory(origin, cookie, startTime, total, done - failed, failed, failedFiles, treeUri.toString(), remoteBase)
            }
            // 全部失败时报 Error，避免用户误以为备份成功
            if (failed == total) {
                val err = "全部 $failed 个文件上传失败（请检查 OpenList 挂载与写入权限）"
                emit(BackupService.Progress.Error(err))
                return Result.Failed(err)
            } else {
                emit(BackupService.Progress.Done(msg))
                return Result.Success
            }
        } catch (e: Exception) {
            val msg = e.message ?: "备份失败"
            emit(BackupService.Progress.Error(msg))
            return Result.Failed(msg)
        }
    }

    private fun emit(p: BackupService.Progress) = BackupService.emitProgress(p)

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

    private fun mkdirRemote(origin: String, cookie: String, remoteBase: String): String? {
        val base = remoteBase.trimEnd('/')
        if (base.isEmpty()) return null
        val url = origin.trimEnd('/') + "/portal/api/files/mkdir?path=" + URLEncoder.encode(base, "UTF-8")
        android.util.Log.i("DDNAS-Backup", "mkdir start: $base")
        val req = Request.Builder().url(url).apply {
            if (cookie.isNotEmpty()) header("Cookie", cookie)
        }.post(EMPTY_BODY).build()
        return try {
            client.newCall(req).execute().use { resp ->
                val body = resp.body?.string() ?: ""
                android.util.Log.i("DDNAS-Backup", "mkdir end: $base code=${resp.code} body=${body.take(160)}")
                if (!resp.isSuccessful) {
                    val hint = if (resp.code == 401) "登录已失效，请重新打开页面后再备份"
                               else "HTTP ${resp.code}"
                    "远程目录不可用：$base（$hint）"
                } else if (!body.contains("\"ok\":true") && !body.contains("\"ok\": true")) {
                    val msg = Regex("\"(?:error|message)\"\\s*:\\s*\"([^\"]+)\"").find(body)?.groupValues?.get(1)
                        ?: body.take(80)
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

    private fun ensureRemoteDirs(
        origin: String, cookie: String, remoteBase: String,
        fullParent: String, cache: MutableSet<String>
    ): String? {
        val base = remoteBase.trimEnd('/')
        if (fullParent.isEmpty() || fullParent == base) return null
        if (base.isNotEmpty() && !fullParent.startsWith(base + "/")) {
            if (fullParent in cache) return null
            cache.add(fullParent)
            return mkdirRemote(origin, cookie, fullParent)?.let { "$fullParent：$it" }
        }
        val rest = if (base.isEmpty()) fullParent.trimStart('/') else fullParent.removePrefix(base + "/")
        val parts = rest.split("/").filter { it.isNotEmpty() }
        var cur = if (base.isEmpty()) "" else base
        for (p in parts) {
            cur = if (cur.isEmpty()) "/$p" else "$cur/$p"
            if (cur in cache) continue
            cache.add(cur)
            val err = mkdirRemote(origin, cookie, cur)
            if (err != null) return "$cur：$err"
        }
        return null
    }

    private fun uploadFile(origin: String, cookie: String, dest: String, file: DocumentFile, done: Int, total: Int): Boolean {
        val url = origin.trimEnd('/') + "/portal/api/files/upload?path=" + URLEncoder.encode(dest, "UTF-8")
        val length = file.length()
        val name = file.name ?: dest.substringAfterLast('/')
        android.util.Log.i("DDNAS-Backup", "upload start: $name size=${fmtBytes(length)} dest=$dest")
        val body = object : RequestBody() {
            override fun contentType() = "application/octet-stream".toMediaType()
            override fun contentLength(): Long = length
            override fun writeTo(sink: okio.BufferedSink) {
                context.contentResolver.openInputStream(file.uri)?.use { input: InputStream ->
                    val buf = ByteArray(64 * 1024)
                    var sent = 0L
                    var lastReport = 0L
                    while (true) {
                        val n = input.read(buf)
                        if (n <= 0) break
                        sink.write(buf, 0, n)
                        sent += n.toLong()
                        if (sent - lastReport >= 1024 * 1024 || sent == length) {
                            lastReport = sent
                            emit(BackupService.Progress.Running(done, total, "$name (${fmtBytes(sent)}/${fmtBytes(length)})"))
                        }
                    }
                } ?: throw IllegalStateException("无法读取文件 $name")
            }
        }
        val req = Request.Builder().url(url).apply {
            if (cookie.isNotEmpty()) header("Cookie", cookie)
        }.post(body).build()
        return try {
            client.newCall(req).execute().use { resp ->
                val ok = resp.isSuccessful
                android.util.Log.i("DDNAS-Backup", "upload end: $name ok=$ok code=${resp.code} sent=${fmtBytes(length)}")
                ok
            }
        } catch (e: Exception) {
            android.util.Log.w("DDNAS-Backup", "upload exception: $name", e)
            throw e
        }
    }

    private fun fmtBytes(b: Long): String {
        if (b < 1024) return b.toString() + "B"
        val kb = b / 1024.0
        if (kb < 1024) return String.format("%.1fKB", kb)
        val mb = kb / 1024.0
        if (mb < 1024) return String.format("%.1fMB", mb)
        return String.format("%.2fGB", mb / 1024.0)
    }

    private fun Log_w(msg: String, e: Exception) {
        android.util.Log.w("DDNAS-Backup", msg, e)
    }

    companion object {
        private val EMPTY_BODY = RequestBody.create(null, ByteArray(0))
    }
}
