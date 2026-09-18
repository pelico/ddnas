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
import okhttp3.RequestBody.Companion.toRequestBody
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

    /** 取消信号：writeTo 循环检测到取消时抛出，立即中断 HTTP 请求体写入，
     *  OkHttp 连接随之断开，不必等整个文件传完才能取消。 */
    private class CancelledException : java.io.IOException("backup cancelled")

    /** 单次上传结果：含 HTTP 状态码，供调用方判断是否值得重试。
     *  - 2xx：成功
     *  - 4xx：客户端错误（登录失效/权限/文件过大），重试也是同样结果，不重试
     *  - 5xx/网络异常：可重试 */
    private data class UploadOutcome(val ok: Boolean, val code: Int)

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
            // 备份前先下载远端 manifest 并合并到本地：app 重装/换设备后本地 manifest 丢失，
            // 从远端拉取前设备的记录可避免全量重传。下载失败（首次备份/文件不存在）不阻断。
            downloadManifest(origin, cookie, remoteBase, manifest)
            val all = ArrayList<Pair<DocumentFile, String>>()
            collect(root, "", all)

            // 增量过滤：只保留需要上传的文件
            // - 失败黑名单：上次上传失败且文件未变更（size+mtime 一致）的直接跳过，
            //   不浪费流量重试必然失败的文件（如云盘不支持的大文件）。
            //   文件一旦修改（size/mtime 变了）自动移出黑名单，重新尝试。
            val toUpload = ArrayList<Pair<DocumentFile, String>>()
            var skipped = 0
            var blacklisted = 0
            for ((file, rel) in all) {
                val size = file.length()
                val mtime = file.lastModified()
                when {
                    manifest.isKnownFailed(rel, size, mtime) -> blacklisted++
                    manifest.needUpload(rel, size, mtime) -> toUpload.add(file to rel)
                    else -> skipped++
                }
            }

            val total = toUpload.size
            if (total == 0) {
                val msg = buildString {
                    append("无需备份（$skipped 个文件未变更")
                    if (blacklisted > 0) append("，$blacklisted 个上次失败已跳过")
                    append("）")
                }
                emit(BackupService.Progress.Done(msg))
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
            var uploaded = 0
            var failed = 0
            var skippedRemote = 0
            // 已创建的远程子目录缓存：根目录已 mkdir 过，加入避免重复
            val mkdirCache = mutableSetOf(remoteBase.trimEnd('/'))
            for ((file, rel) in toUpload) {
                // 用户点"取消备份"后，等当前文件传完即退出循环
                if (BackupService.isCancelled()) {
                    emit(BackupService.Progress.Done("已取消（已传 $uploaded 个文件）"))
                    return Result.Cancelled
                }
                val dest = (remoteBase.trimEnd('/') + "/" + rel).replace(Regex("/+"), "/")
                val localSize = file.length()
                // 增量兜底：manifest 以 treeUri+remoteBase 的 hashCode 作命名空间，
                // 重新选本地目录(新 treeUri)或重新浏览远端(改 remoteBase 写法)都会让
                // hashCode 变 → 命中不到旧记录 → 误判为待传。这里再以远端实际存在性
                // 兜底：同名且同大小视为已备份，跳过，避免重复上传已存在的文件。
                if (remoteFileSize(origin, cookie, dest) == localSize) {
                    manifest.markUploaded(rel, localSize, file.lastModified())
                    skippedRemote++; done++
                    emit(BackupService.Progress.Running(done, total, file.name ?: rel))
                    continue
                }
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
                // 取消信号 / 4xx 客户端错误不重试，避免大文件白白重传一遍
                var ok = false
                var lastCode = 0
                for (attempt in 0..2) {
                    if (BackupService.isCancelled()) break
                    var outcome: UploadOutcome? = null
                    try {
                        outcome = uploadFile(origin, cookie, dest, file, done, total)
                        ok = outcome.ok
                        lastCode = outcome.code
                    } catch (e: CancelledException) {
                        // 用户取消：writeTo 循环已中断写入，立即停止，不再重试
                        emit(BackupService.Progress.Done("已取消（已传 $uploaded 个文件）"))
                        return Result.Cancelled
                    } catch (e: Exception) {
                        Log_w("upload attempt ${attempt + 1} fail: $rel", e)
                        ok = false
                        lastCode = 0
                    }
                    if (ok) break
                    // 4xx 客户端错误（401 登录失效/403 权限/413 文件过大等）：
                    // 重试也是同样结果，且大文件重传浪费流量，直接放弃重试
                    if (outcome != null && outcome.code in 400..499) {
                        break
                    }
                    if (attempt < 2) {
                        val backoff = (1000L shl attempt)
                        emit(BackupService.Progress.Running(done, total, "重试(${attempt + 1}/3) ${file.name ?: rel}"))
                        try { kotlinx.coroutines.delay(backoff) } catch (_: Exception) { break }
                    }
                }
                if (ok) {
                    manifest.markUploaded(rel, file.length(), file.lastModified())
                    uploaded++
                } else {
                    failed++
                    failedFiles.add(rel)
                    // 永久错误加入失败黑名单：文件未变更的话下次备份直接跳过，
                    // 不浪费流量重试必然失败的文件（如云盘不支持的大文件）。
                    // - 4xx 客户端错误（413 文件过大/403 权限不足等；401 登录失效除外，
                    //   那是临时态，重新登录后应重试）
                    // - 500 业务失败（OpenList/存储驱动拒绝上传，如不支持大文件）
                    // 网络异常（code=0）/ 5xx 网关错误（502/503/504）是临时问题，下次应重试。
                    val isPermanent = (lastCode in 400..499 && lastCode != 401) || lastCode == 500
                    if (isPermanent) {
                        manifest.markFailed(rel, file.length(), file.lastModified())
                    }
                }
                done++
                emit(BackupService.Progress.Running(done, total, file.name ?: rel))
            }
            val msg = buildString {
                append("备份完成：上传 $uploaded 个文件")
                if (skipped + skippedRemote > 0) append("，跳过 ${skipped + skippedRemote} 个（未变更/远端已存在）")
                if (blacklisted > 0) append("，$blacklisted 个上次失败已跳过")
                if (failed > 0) append("，失败 $failed 个")
            }
            // 上报备份历史到中间件 SQLite
            if (reportHistory) {
                reportHistory(origin, cookie, startTime, total, done - failed, failed, failedFiles, treeUri.toString(), remoteBase)
            }
            // 备份完成后上传 manifest 到远端：下次备份前下载合并，app 重装/换设备不丢增量历史。
            // 即使部分文件失败也上传——已成功的条目 markUploaded 了，失败的下次重试。
            uploadManifest(origin, cookie, remoteBase, manifest)
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

    /** 查询远端文件大小；不存在/出错/是目录返回 null。
     *  增量备份兜底用：manifest 命中不到时再以远端实际存在性判断，避免重复上传已存在文件。 */
    private fun remoteFileSize(origin: String, cookie: String, dest: String): Long? {
        val url = origin.trimEnd('/') + "/portal/api/files/get?path=" + URLEncoder.encode(dest, "UTF-8")
        val req = Request.Builder().url(url).apply {
            if (cookie.isNotEmpty()) header("Cookie", cookie)
        }.get().build()
        return try {
            client.newCall(req).execute().use { resp ->
                if (!resp.isSuccessful) return@use null
                val body = resp.body?.string() ?: return@use null
                // OpenList /files/get 返回 {name,size,is_dir,...}；目录不算
                if (Regex("\"is_dir\"\\s*:\\s*true").containsMatchIn(body)) return@use null
                Regex("\"size\"\\s*:\\s*(\\d+)").find(body)?.groupValues?.get(1)?.toLongOrNull()
            }
        } catch (_: Exception) { null }
    }

    /** manifest 远端路径：<remoteBase>/.ddnas_manifest.json
     *  作为隐藏文件放在备份根目录下，不干扰用户文件。 */
    private fun manifestRemotePath(remoteBase: String): String {
        return remoteBase.trimEnd('/') + "/.ddnas_manifest.json"
    }

    /** 下载远端 manifest 并合并到本地。
     *  备份前调用：app 重装/换设备后本地 manifest 为空，从远端拉取前设备的记录
     *  避免全量重传。下载失败（首次备份/文件不存在/网络异常）不阻断，返回继续。 */
    private fun downloadManifest(origin: String, cookie: String, remoteBase: String, manifest: BackupManifest) {
        val path = manifestRemotePath(remoteBase)
        // stream 端点路径格式：/portal/api/files/stream/<各段URL编码后用/连接>
        val segs = path.split("/").filter { it.isNotEmpty() }
            .joinToString("/") { URLEncoder.encode(it, "UTF-8") }
        val url = origin.trimEnd('/') + "/portal/api/files/stream/" + segs
        val req = Request.Builder().url(url).apply {
            if (cookie.isNotEmpty()) header("Cookie", cookie)
        }.get().build()
        try {
            client.newCall(req).execute().use { resp ->
                if (!resp.isSuccessful) return
                val body = resp.body?.string() ?: return
                manifest.mergeFromJson(body)
                android.util.Log.i("DDNAS-Backup", "manifest downloaded and merged from remote")
            }
        } catch (e: Exception) {
            android.util.Log.w("DDNAS-Backup", "manifest download failed (ok for first backup): ${e.message}")
        }
    }

    /** 上传本地 manifest 到远端。
     *  备份后调用：把当前 manifest 序列化为 JSON 上传到 <remoteBase>/.ddnas_manifest.json，
     *  下次备份前下载合并。即使部分文件失败也上传——已成功条目 markUploaded 了，失败下次重试。 */
    private fun uploadManifest(origin: String, cookie: String, remoteBase: String, manifest: BackupManifest) {
        val path = manifestRemotePath(remoteBase)
        val json = manifest.toJson()
        val url = origin.trimEnd('/') + "/portal/api/files/upload?path=" + URLEncoder.encode(path, "UTF-8")
        val body = json.toByteArray(Charsets.UTF_8)
            .toRequestBody("application/octet-stream".toMediaType())
        val req = Request.Builder().url(url).apply {
            if (cookie.isNotEmpty()) header("Cookie", cookie)
        }.post(body).build()
        try {
            client.newCall(req).execute().use { resp ->
                val respBody = resp.body?.string() ?: ""
                if (resp.isSuccessful && respBody.contains("\"ok\":true")) {
                    android.util.Log.i("DDNAS-Backup", "manifest uploaded to remote: $path")
                } else {
                    android.util.Log.w("DDNAS-Backup", "manifest upload failed: ${resp.code} $respBody")
                }
            }
        } catch (e: Exception) {
            android.util.Log.w("DDNAS-Backup", "manifest upload exception: ${e.message}")
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

    private fun uploadFile(origin: String, cookie: String, dest: String, file: DocumentFile, done: Int, total: Int): UploadOutcome {
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
                        // 取消立即生效：每读一块都检查标志，一旦取消抛
                        // CancelledException 中断写入，OkHttp 连接随之断开，
                        // 不必等整个文件传完才能取消。
                        if (BackupService.isCancelled()) throw CancelledException()
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
                val respBody = resp.body?.string() ?: ""
                // 不只看 HTTP code：OpenList/AList HTTP 恒 200，业务结果在 body 的
                // {"code":xxx,"message":...}。中间件 handleUpload 已把业务失败转成
                // {"ok":false,"error":...}。这里双重校验：
                // 1) HTTP 非 2xx → 失败（用 resp.code 让重试逻辑判断 4xx 不重试）
                // 2) body 不含 "ok":true → 失败（业务层失败，如空间不足/权限/路径非法）
                val httpOk = resp.isSuccessful
                val bizOk = httpOk && respBody.contains("\"ok\":true")
                android.util.Log.i("DDNAS-Backup", "upload end: $name httpOk=$httpOk bizOk=$bizOk code=${resp.code} body=${respBody.take(120)} sent=${fmtBytes(length)}")
                if (httpOk && !bizOk) {
                    // HTTP 200 但业务失败：提取 error message，避免谎报成功后
                    // markUploaded 跳过该文件 → 数据丢失。
                    // 用 code=500 让重试逻辑走"5xx 不重试"路径（业务失败重试无意义）
                    UploadOutcome(false, 500)
                } else {
                    UploadOutcome(bizOk, resp.code)
                }
            }
        } catch (e: CancelledException) {
            // writeTo 检测到取消：向上抛，由 runBackup 重试循环捕获后立即返回
            throw e
        } catch (e: Exception) {
            android.util.Log.w("DDNAS-Backup", "upload exception: $name", e)
            // 网络异常视为可重试：code=0 表示无 HTTP 响应（连接超时/断开等）
            UploadOutcome(false, 0)
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