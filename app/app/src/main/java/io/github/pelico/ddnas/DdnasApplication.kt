package io.github.pelico.ddnas

import android.app.Application
import android.app.NotificationChannel
import android.app.NotificationManager
import android.os.Build
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import okhttp3.OkHttpClient

/**
 * 全局：共享 OkHttpClient、应用级协程作用域、备份进度状态。
 *
 * 套壳架构下 App 不再存 Bearer token：WebView 与中间件 /portal 同源，
 * 用 admin cookie 会话鉴权；ExoPlayer 与备份服务通过 CookieManager 取
 * 该会话 cookie 注入到各自 OkHttp 请求头。
 */
class DdnasApplication : Application() {

    val appScope = CoroutineScope(SupervisorJob() + Dispatchers.IO)

    /** 供 ExoPlayer 与备份服务复用。各调用方按需加 cookie 拦截器。 */
    val okHttpClient: OkHttpClient by lazy {
        OkHttpClient.Builder().build()
    }

    override fun onCreate() {
        super.onCreate()
        instance = this
        createBackupChannel()
    }

    private fun createBackupChannel() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            val ch = NotificationChannel(
                CHANNEL_BACKUP, "备份任务", NotificationManager.IMPORTANCE_LOW
            ).apply { description = "文件备份上传进度" }
            getSystemService(NotificationManager::class.java).createNotificationChannel(ch)
        }
    }

    companion object {
        const val CHANNEL_BACKUP = "ddnas_backup"
        lateinit var instance: DdnasApplication
            private set
    }
}
