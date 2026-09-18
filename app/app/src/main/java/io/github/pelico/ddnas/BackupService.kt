package io.github.pelico.ddnas

import android.app.Notification
import android.app.Service
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.Uri
import android.os.Build
import android.os.IBinder
import androidx.core.app.NotificationCompat
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import okhttp3.OkHttpClient

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

    /**
     * 前台备份入口：创建 [BackupEngine] 执行上传（this 为系统挂载的合法 Context）。
     * 上传期间订阅进度刷新前台通知，结束后 stopSelf。
     */
    private suspend fun runBackup(treeUri: Uri, origin: String, cookie: String, remoteBase: String) {
        val engine = BackupEngine(this, client)
        // 订阅共享进度流，刷新前台通知进度条
        val notifJob = app.appScope.launch {
            BackupService.progress.collect { p ->
                if (p is BackupService.Progress.Running) {
                    startForegroundCompat(NOTIF_ID, buildNotification("备份中", p.done, p.total))
                }
            }
        }
        try {
            val result = engine.runBackup(treeUri, origin, cookie, remoteBase, reportHistory = true)
            // 手动备份成功也写入上次备份时间，与自动备份保持一致
            if (result is BackupEngine.Result.Success) {
                try { io.github.pelico.ddnas.data.BackupStore(this).setLastBackupTime(System.currentTimeMillis()) } catch (_: Exception) {}
            }
        } finally {
            notifJob.cancel()
            running = false
            cancelled = false
            stopSelf()
        }
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
        private val _progress = MutableStateFlow<Progress>(Progress.Idle)
        val progress: StateFlow<Progress> = _progress

        // 运行中标志：onStartCommand 拒绝重复启动，startBackupService 前置检查
        @Volatile private var running = false
        // 取消标志：用户点"取消备份"后置 true，runBackup 循环检测后退出
        @Volatile private var cancelled = false
        fun isRunning(): Boolean = running
        fun cancel() { cancelled = true }

        // 供 BackupEngine 读取取消标志
        fun isCancelled(): Boolean = cancelled
        // 供 BackupEngine/Service 写入进度（_progress 对外部不可写）
        internal fun emitProgress(p: Progress) { _progress.value = p }
        // Service 启动/结束时设置运行标志，避免并发备份
        fun setRunning(v: Boolean) { running = v }
        fun setCancelled(v: Boolean) { cancelled = v }
    }
}
