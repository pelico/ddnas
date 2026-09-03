package io.github.pelico.ddnas

import android.app.Activity
import android.os.Bundle
import android.view.View
import android.view.WindowManager
import android.webkit.CookieManager
import android.webkit.WebChromeClient
import android.webkit.WebView
import android.webkit.WebViewClient

/**
 * 图片预览：全屏 WebView 加载 /portal/api/openlist/files/stream/...，
 * 注入 WebView 会话 cookie（同源），支持双指/按钮缩放。
 *
 * App 内 ddnas.viewImage(url, name) 启动此 Activity。
 */
class ImageActivity : Activity() {

    private lateinit var webView: WebView

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
        // 沉浸式全屏
        window.decorView.systemUiVisibility = (
            View.SYSTEM_UI_FLAG_FULLSCREEN or
            View.SYSTEM_UI_FLAG_HIDE_NAVIGATION or
            View.SYSTEM_UI_FLAG_IMMERSIVE_STICKY or
            View.SYSTEM_UI_FLAG_LAYOUT_STABLE or
            View.SYSTEM_UI_FLAG_LAYOUT_HIDE_NAVIGATION or
            View.SYSTEM_UI_FLAG_LAYOUT_FULLSCREEN
        )

        val url = intent.getStringExtra(EXTRA_URL) ?: run { finish(); return }
        val cookie = intent.getStringExtra(EXTRA_COOKIE) ?: ""

        // 同源注入 cookie，确保 stream 请求通过鉴权
        if (cookie.isNotEmpty()) {
            CookieManager.getInstance().setAcceptCookie(true)
            val origin = Uri.parse(url)
            val domain = origin.host ?: ""
            // CookieManager.setCookie 需要带 scheme 的 url
            val cookieUrl = (origin.scheme ?: "http") + "://" + domain +
                (if (origin.port != -1) ":" + origin.port else "")
            CookieManager.getInstance().setCookie(cookieUrl, cookie)
            CookieManager.getInstance().flush()
        }

        webView = WebView(this).apply {
            settings.javaScriptEnabled = false
            settings.builtInZoomControls = true
            settings.displayZoomControls = false
            settings.useWideViewPort = true
            settings.loadWithOverviewMode = true
            settings.cacheMode = android.webkit.WebSettings.LOAD_NO_CACHE
            webViewClient = WebViewClient()
            webChromeClient = WebChromeClient()
        }
        setContentView(webView)
        // 用 <img> 包一层确保按内容尺寸自适应，避免 WebView 默认 viewport 拉伸
        val html = "<!doctype html><html><head><meta charset='utf-8'>" +
            "<meta name='viewport' content='width=device-width,initial-scale=1,user-scalable=yes'>" +
            "<style>html,body{margin:0;background:#000}img{display:block;max-width:100%;max-height:100vh;width:auto;height:auto;margin:0 auto;object-fit:contain}</style>" +
            "</head><body><img src='" + url.replace("'", "\\'") + "'></body></html>"
        webView.loadDataWithBaseURL(url.substringBefore("/portal/"), html, "text/html", "UTF-8", null)
    }

    override fun onDestroy() {
        super.onDestroy()
        webView.destroy()
    }

    companion object {
        const val EXTRA_URL = "image_url"
        const val EXTRA_COOKIE = "cookie"
    }
}
