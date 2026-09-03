package io.github.pelico.ddnas

import android.app.Activity
import android.app.AlertDialog
import android.net.Uri
import android.os.Bundle
import android.util.Log
import android.view.WindowManager
import androidx.media3.common.MediaItem
import androidx.media3.common.PlaybackException
import androidx.media3.common.Player
import androidx.media3.datasource.DataSource
import androidx.media3.datasource.okhttp.OkHttpDataSource
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.DefaultMediaSourceFactory
import androidx.media3.ui.PlayerView
import okhttp3.OkHttpClient

/**
 * 流式播放：ExoPlayer 通过 OkHttp 拉取 /portal/api/openlist/files/stream/...，
 * 注入 WebView 登录后的 admin 会话 cookie，使 Range 请求同样鉴权。
 */
class PlayerActivity : Activity() {

    private var player: ExoPlayer? = null

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        // 播放期间保持屏幕常亮
        window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)

        val streamUrl = intent.getStringExtra(EXTRA_URL) ?: run { finish(); return }
        val host = intent.getStringExtra(EXTRA_HOST) ?: run { finish(); return }
        val cookie = intent.getStringExtra(EXTRA_COOKIE) ?: ""

        Log.i(TAG, "start stream url=$streamUrl host=$host cookieLen=${cookie.length}")

        val client = buildAuthedClient(host, cookie)
        val view = PlayerView(this).also { v ->
            v.useController = true
            v.player = newPlayer(client, streamUrl).also { player = it }
        }
        setContentView(view)
    }

    private fun buildAuthedClient(host: String, cookie: String): OkHttpClient {
        // 复用全局 client 的连接池等基础配置，仅追加 cookie 头拦截器。
        val base = (application as DdnasApplication).okHttpClient
        return base.newBuilder()
            .addInterceptor { chain ->
                val req = chain.request()
                // 仅对中间件主机的请求注入 cookie，避免泄漏到其他域。
                val target = req.url.host
                val originHost = Uri.parse(host)?.host ?: host
                if (target == originHost && cookie.isNotEmpty()) {
                    Log.d(TAG, "inject cookie for $target (len=${cookie.length})")
                    chain.proceed(req.newBuilder().header("Cookie", cookie).build())
                } else {
                    Log.d(TAG, "skip cookie: target=$target origin=$originHost cookieLen=${cookie.length}")
                    chain.proceed(req)
                }
            }
            .build()
    }

    private fun newPlayer(client: OkHttpClient, url: String): ExoPlayer {
        // OkHttp client 已在 buildAuthedClient 里挂了 cookie 拦截器，
        // ExoPlayer 发 Range 请求时拦截器自动注入 admin 会话 cookie。
        val factory: DataSource.Factory = OkHttpDataSource.Factory(client)
        val mediaSourceFactory = DefaultMediaSourceFactory(this).setDataSourceFactory(factory)
        return ExoPlayer.Builder(this)
            .setMediaSourceFactory(mediaSourceFactory)
            .build()
            .also { p ->
                p.addListener(object : Player.Listener {
                    override fun onPlayerErrorChanged(error: PlaybackException?) {
                        if (error != null) {
                            Log.e(TAG, "player error", error)
                            showError("播放失败", error.message ?: error.errorCodeName)
                        }
                    }
                })
                p.setMediaItem(MediaItem.fromUri(url))
                p.prepare()
                p.playWhenReady = true
            }
    }

    private fun showError(title: String, msg: String) {
        runOnUiThread {
            AlertDialog.Builder(this)
                .setTitle(title)
                .setMessage(msg + "\n\n请查看 docker logs [openlist] 日志确认后端是否正常。\ncookie/鉴权问题多为 401。")
                .setPositiveButton("关闭") { _, _ -> finish() }
                .show()
        }
    }

    override fun onDestroy() {
        super.onDestroy()
        player?.release()
        player = null
    }

    companion object {
        private const val TAG = "DDNAS-Player"
        const val EXTRA_URL = "stream_url"
        const val EXTRA_HOST = "host"
        const val EXTRA_COOKIE = "cookie"
    }
}
