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
        // 用 WebView 真实 UA：Cloudflare Tunnel 的 Bot Fight Mode / WAF 会拦截
        // okhttp/4.x 这类非浏览器 UA，返回挑战页 HTML（<!doctype html>...），
        // 导致备份的 mkdir/upload 原生请求失败（WebView 的 fetch 用浏览器 UA
        // 正常穿透，所以前端验证成功但点备份报错）。换浏览器 UA 后原生请求
        // 与 WebView 在 CF 侧视为同一类客户端，可一起穿透。
        val ua = android.webkit.WebSettings.getDefaultUserAgent(this)
        OkHttpClient.Builder()
            .addInterceptor { chain ->
                chain.proceed(chain.request().newBuilder().header("User-Agent", ua).build())
            }
            .build()
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
