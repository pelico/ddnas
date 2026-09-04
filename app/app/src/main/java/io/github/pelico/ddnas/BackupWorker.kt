package io.github.pelico.ddnas

import android.content.Context
import android.net.Uri
import android.webkit.CookieManager
import androidx.work.BackoffPolicy
import androidx.work.Constraints
import androidx.work.CoroutineWorker
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.NetworkType
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import io.github.pelico.ddnas.data.BackupStore
import io.github.pelico.ddnas.data.ServerStore
import kotlinx.coroutines.flow.first
import java.util.concurrent.TimeUnit

/**
 * WorkManager 定时增量备份 Worker。
 *
 * - 读取 [BackupStore] 获取 treeUri / remoteBase，读取 [ServerStore] 获取活跃服务器。
 * - 复用 [BackupService] 的上传逻辑（mkdir + 增量 + 重试）。
 * - 约束：仅充电 + 仅 Wi-Fi（UNMETERED），避免在移动数据下跑大文件。
 * - 周期：15 分钟（WorkManager 最小值），实际由系统 doze 调度。
 */
class BackupWorker(
    context: Context,
    params: WorkerParameters
) : CoroutineWorker(context, params) {

    override suspend fun doWork(): Result {
        val ctx = applicationContext
        val store = BackupStore(ctx)
        val cfg = store.get()

        // 没选目录或没开自动备份 → 跳过
        if (cfg.treeUri.isEmpty() || !cfg.autoBackup) return Result.success()

        // 找活跃服务器（servers/activeIndex 是 Flow，doWork 是 suspend 可直接 .first()）
        val serverStore = ServerStore(ctx)
        val servers = serverStore.servers.first()
        val activeIdx = serverStore.activeIndex.first()
        val active = servers.getOrNull(activeIdx) ?: return Result.success()

        val cookie = CookieManager.getInstance().getCookie(active.url) ?: ""
        val origin = active.url.trimEnd('/')

        // 直接调 BackupService 的上传逻辑（不经过 Service 组件）
        return try {
            val service = BackupService()
            service.runBackupForWorker(
                Uri.parse(cfg.treeUri),
                origin,
                cookie,
                cfg.remoteBase
            )
            store.setLastBackupTime(System.currentTimeMillis())
            Result.success()
        } catch (e: Exception) {
            // 失败时 WorkManager 按 backoff 自动重试
            Result.retry()
        }
    }

    companion object {
        private const val WORK_NAME = "ddnas_auto_backup"

        /**
         * 启动或更新定时备份任务（15 分钟周期，充电 + Wi-Fi 约束）。
         * 在用户开启 autoBackup 时调用。
         */
        fun enable(context: Context) {
            val constraints = Constraints.Builder()
                .setRequiresCharging(true)
                .setRequiredNetworkType(NetworkType.UNMETERED)
                .build()

            val req = PeriodicWorkRequestBuilder<BackupWorker>(15, TimeUnit.MINUTES)
                .setConstraints(constraints)
                .setBackoffCriteria(BackoffPolicy.EXPONENTIAL, 1, TimeUnit.MINUTES)
                .build()

            WorkManager.getInstance(context).enqueueUniquePeriodicWork(
                WORK_NAME,
                ExistingPeriodicWorkPolicy.UPDATE,
                req
            )
        }

        /** 取消定时备份（用户关闭 autoBackup 时调用）。 */
        fun disable(context: Context) {
            WorkManager.getInstance(context)
                .cancelUniqueWork(WORK_NAME)
        }
    }
}
