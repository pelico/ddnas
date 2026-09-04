package io.github.pelico.ddnas

import android.content.Intent
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.webkit.CookieManager
import android.webkit.JavascriptInterface
import android.webkit.ValueCallback
import android.webkit.WebChromeClient
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.result.ActivityResultLauncher
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.ArrowBack
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.Dns
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FloatingActionButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.lifecycleScope
import io.github.pelico.ddnas.data.Server
import io.github.pelico.ddnas.data.ServerStore
import io.github.pelico.ddnas.ui.theme.DDNASTheme
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch

/**
 * App 主体：WebView 套壳加载中间件 /portal（cookie 会话鉴权），
 * 通过 ddnas JS 桥接原生 ExoPlayer 播放与 SAF 备份。
 * 支持多服务器：ServerStore 持久化列表，可新增/编辑/删除/切换。
 */
class MainActivity : ComponentActivity() {

    private val store by lazy { ServerStore(this) }
    private val backupStore by lazy { io.github.pelico.ddnas.data.BackupStore(this) }

    private lateinit var treePicker: ActivityResultLauncher<Uri?>
    private lateinit var notifPermission: ActivityResultLauncher<String>
    private lateinit var fileChooser: ActivityResultLauncher<String>
    // WebView 文件选择回调，由 WebChromeClient.onShowFileChooser 设置
    private var filePathCallback: ValueCallback<Array<Uri>>? = null
    // 系统返回/侧滑返回处理入口：先 WebView 历史 → 再 H5 路由 → 最后 finish
    // 需要持有 PortalWebView 实例，因此在 AndroidView factory 创建时回填。
    private var portalWebView: WebView? = null

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        treePicker = registerForActivityResult(ActivityResultContracts.OpenDocumentTree()) { uri ->
            if (uri != null) onTreePicked(uri)
        }
        notifPermission = registerForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
            if (!granted) { pendingBackupAfterPerm = false; return@registerForActivityResult }
            // 通知权限通过：若 pendingBackupAfterPerm 为 true，按既有备份目录直接备份；
            // 否则走首次选目录流程
            val cfg = kotlinx.coroutines.runBlocking { backupStore.get() }
            if (pendingBackupAfterPerm && cfg.treeUri.isNotEmpty()) {
                pendingBackupAfterPerm = false
                startBackupService(cfg.treeUri, cfg.remoteBase)
            } else {
                pendingBackupAfterPerm = false
                treePicker.launch(null)
            }
        }
        // <input type="file"> 选择回调，portal 上传按钮需要
        fileChooser = registerForActivityResult(ActivityResultContracts.GetContent()) { uri ->
            filePathCallback?.onReceiveValue(if (uri != null) arrayOf(uri) else null)
            filePathCallback = null
        }

        // 系统返回/侧滑返回：先 WebView 历史，再 H5 内部路由（文件页层级/切Tab），最后 finish()
        // 避免"在深层目录里滑一下就退到桌面"的割裂体验。
        onBackPressedDispatcher.addCallback(this,
            object : androidx.activity.OnBackPressedCallback(true) {
                override fun handleOnBackPressed() {
                    val wv = portalWebView
                    if (wv != null && wv.canGoBack()) {
                        wv.goBack()
                        return
                    }
                    // 先问 H5 __onNativeBack 能不能在内部消耗（文件页 goUp / 切首页 / 关弹层）
                    if (wv != null) {
                        var consumed = false
                        wv.evaluateJavascript(
                            "(function(){try{return __onNativeBack();}catch(e){return 'not_handled';}})()"
                        ) { result ->
                            val r = result?.trim('"') ?: "not_handled"
                            if (r == "handled") return@evaluateJavascript
                            // H5 也没有可消费的 → 退出 Activity
                            finish()
                        }
                        return
                    }
                    finish()
                }
            }
        )

        setContent {
            DDNASTheme { AppRoot() }
        }
    }

    @OptIn(ExperimentalMaterial3Api::class)
    @Composable
    private fun AppRoot() {
        val servers by store.servers.collectAsStateWithLifecycle(initialValue = emptyList())
        val activeIdx by store.activeIndex.collectAsStateWithLifecycle(initialValue = 0)
        var showManager by remember { mutableStateOf(false) }
        val backup by BackupService.progress.collectAsStateWithLifecycle()

        val active = servers.getOrNull(activeIdx)

        Scaffold(
            modifier = Modifier.fillMaxSize(),
            topBar = {
                if (active != null) {
                    TopAppBar(
                        title = { Text(active.name) },
                        actions = {
                            IconButton(onClick = { showManager = true }) {
                                Icon(Icons.Filled.Dns, contentDescription = "切换服务器")
                            }
                        }
                    )
                }
            }
        ) { inner ->
            Box(Modifier.fillMaxSize().padding(inner)) {
                if (active == null || showManager) {
                    ServerManager(fullScreen = active == null, onClose = { showManager = false })
                }
                if (active != null && !showManager) {
                    PortalWebView(server = active, backupProgress = backup)
                }
            }
        }
    }

    /** 加载 /portal 的 WebView，注入 ddnas 桥。按服务器 url+name 为 key 重建以切换。
     *  backupProgress 由 BackupService 推送，通过 evaluateJavascript 注入 portal，
     *  让备份进度内嵌在备份面板里显示，不再弹 AlertDialog。 */
    @Composable
    private fun PortalWebView(server: Server, backupProgress: BackupService.Progress) {
        // 切换服务器时整体重建 WebView，避免复用上一个会话的 cookie/JS 状态。
        key(server.url + server.name) {
            AndroidView(
                // 避开系统导航栏，防止 tabbar 被手势条/虚拟键遮挡，内容也能拉到底
                modifier = Modifier.fillMaxSize().navigationBarsPadding(),
                factory = { ctx ->
                    WebView(ctx).apply {
                        settings.javaScriptEnabled = true
                        settings.domStorageEnabled = true
                        settings.allowFileAccess = false
                        // Console 消息转发到 Logcat（tag=DDNAS-Console），
                        // 便于诊断 portal 前端在 WebView 内的运行情况（fetch 失败、JS 报错等）。
                        webChromeClient = object : WebChromeClient() {
                            override fun onConsoleMessage(message: android.webkit.ConsoleMessage?): Boolean {
                                val m = message ?: return true
                                android.util.Log.i(
                                    "DDNAS-Console",
                                    "[${m.messageLevel()}] ${m.message()} @ ${m.sourceId()}:${m.lineNumber()}"
                                )
                                return true
                            }
                            override fun onShowFileChooser(
                                webView: WebView?,
                                callback: ValueCallback<Array<Uri>>?,
                                params: FileChooserParams?
                            ): Boolean {
                                filePathCallback?.onReceiveValue(null)
                                filePathCallback = callback
                                return try {
                                    fileChooser.launch("*/*")
                                    true
                                } catch (e: Exception) {
                                    filePathCallback = null
                                    false
                                }
                            }
                        }
                        webViewClient = WebViewClient()
                        CookieManager.getInstance().setAcceptCookie(true)
                        CookieManager.getInstance().setAcceptThirdPartyCookies(this, true)
                        addJavascriptInterface(Bridge(), "ddnas")
                        loadUrl(server.url.trimEnd('/') + "/portal")
                        // 系统返回键要拿 WebView 实例：onBackPressedDispatcher 回调里用
                        portalWebView = this
                    }
                },
                update = { wv ->
                    // 重建/更新时保持 Activity 级引用同步
                    portalWebView = wv
                    // BackupService 进度变化时，推送到 portal 备份面板内嵌显示。
                    // Idle 不推送（避免覆盖面板默认状态）。
                    if (backupProgress !is BackupService.Progress.Idle) {
                        val js = "window.__onBackupProgress&&__onBackupProgress(" +
                            backupProgress.toJson() + ")"
                        wv.evaluateJavascript(js, null)
                    }
                }
            )
        }
    }

    // --- 多服务器管理 UI ---

    @OptIn(ExperimentalMaterial3Api::class)
    @Composable
    private fun ServerManager(fullScreen: Boolean, onClose: () -> Unit) {
        val servers by store.servers.collectAsStateWithLifecycle(initialValue = emptyList())
        val activeIdx by store.activeIndex.collectAsStateWithLifecycle(initialValue = 0)
        var editing by remember { mutableStateOf<Pair<Int, Server>?>(null) }
        var name by remember { mutableStateOf("") }
        var url by remember { mutableStateOf("") }

        Scaffold(
            topBar = {
                TopAppBar(
                    title = { Text("服务器") },
                    navigationIcon = {
                        if (!fullScreen) {
                            IconButton(onClick = onClose) {
                                Icon(Icons.Filled.ArrowBack, contentDescription = "返回")
                            }
                        }
                    }
                )
            },
            floatingActionButton = {
                FloatingActionButton(onClick = {
                    editing = Pair(-1, Server("", "")); name = ""; url = ""
                }) { Icon(Icons.Filled.Add, contentDescription = "添加服务器") }
            }
        ) { inner ->
            if (servers.isEmpty()) {
                Box(Modifier.fillMaxSize().padding(inner), contentAlignment = Alignment.Center) {
                    Text("暂无服务器，点右下角 + 添加", color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
            } else {
                LazyColumn(
                    Modifier.fillMaxSize().padding(inner),
                    verticalArrangement = Arrangement.spacedBy(8.dp)
                ) {
                    itemsIndexed(servers) { idx, sv ->
                        Row(
                            Modifier.fillMaxWidth().padding(horizontal = 12.dp),
                            verticalAlignment = Alignment.CenterVertically
                        ) {
                            Column(Modifier.weight(1f)) {
                                Text(sv.name, style = MaterialTheme.typography.bodyLarge)
                                Text(sv.url, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                            }
                            if (idx == activeIdx) Text("●", color = MaterialTheme.colorScheme.primary)
                            IconButton(onClick = { editing = Pair(idx, sv); name = sv.name; url = sv.url }) {
                                Text("编辑")
                            }
                            IconButton(onClick = { lifecycleScope.launch { store.delete(idx) } }) {
                                Icon(Icons.Filled.Delete, contentDescription = "删除")
                            }
                            TextButton(onClick = {
                                lifecycleScope.launch { store.setActive(idx) }
                                onClose()
                            }) { Text("使用") }
                        }
                    }
                }
            }
        }

        editing?.let { (idx, _) ->
            AlertDialog(
                onDismissRequest = { editing = null },
                title = { Text(if (idx < 0) "添加服务器" else "编辑服务器") },
                text = {
                    Column {
                        OutlinedTextField(value = name, onValueChange = { name = it }, label = { Text("名称") }, singleLine = true, modifier = Modifier.fillMaxWidth())
                        OutlinedTextField(value = url, onValueChange = { url = it }, label = { Text("地址 http://...") }, singleLine = true, modifier = Modifier.fillMaxWidth())
                    }
                },
                confirmButton = {
                    TextButton(onClick = {
                        val n = name.trim(); val u = url.trim()
                        if (n.isEmpty() || u.isEmpty()) return@TextButton
                        lifecycleScope.launch {
                            if (idx < 0) store.add(n, u) else store.update(idx, n, u)
                            editing = null
                        }
                    }) { Text("保存") }
                },
                dismissButton = { TextButton(onClick = { editing = null }) { Text("取消") } }
            )
        }
    }

    // --- JS 桥 ---

    /** 暴露给 /portal 页面的原生桥：ddnas.playMedia(url) / ddnas.startBackup()。 */
    private inner class Bridge {
        @JavascriptInterface
        fun playMedia(url: String) {
            runOnUiThread { startPlayer(url) }
        }

        /** 立即备份入口。返回状态字符串供前端提示：
         *  "running" 已有备份在跑；"started" 已发起（含权限申请中/选目录中）；"noDir" 无目录已弹选择器。 */
        @JavascriptInterface
        fun startBackup(): String {
            if (BackupService.isRunning()) return "running"
            runOnUiThread {
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU &&
                    checkSelfPermission(android.Manifest.permission.POST_NOTIFICATIONS) !=
                    android.content.pm.PackageManager.PERMISSION_GRANTED
                ) {
                    pendingBackupAfterPerm = true
                    notifPermission.launch(android.Manifest.permission.POST_NOTIFICATIONS)
                    return@runOnUiThread
                }
                // 已有备份目录：直接增量备份；否则首次选目录
                val cfg = kotlinx.coroutines.runBlocking { backupStore.get() }
                if (cfg.treeUri.isNotEmpty()) {
                    startBackupService(cfg.treeUri, cfg.remoteBase)
                } else {
                    treePicker.launch(null)
                }
            }
            return "started"
        }

        /** 取消正在进行的备份。runBackup 在当前文件传完后退出循环。 */
        @JavascriptInterface
        fun cancelBackup() {
            BackupService.cancel()
        }

        /** 选择本地备份源目录（SAF）。选择后持久化 treeUri，下次直接复用。 */
        @JavascriptInterface
        fun chooseBackupDir() {
            runOnUiThread { treePicker.launch(null) }
        }

        /** 返回备份配置 JSON：{hasDir,dirDisplay,remoteBase,autoBackup,lastBackupTime}。
         *  portal 我的页用此渲染备份面板。 */
        @JavascriptInterface
        fun getBackupConfig(): String {
            val cfg = kotlinx.coroutines.runBlocking { backupStore.get() }
            val dir = treeUriDisplay(cfg.treeUri)
            val hasDir = cfg.treeUri.isNotEmpty()
            // 校验持久化权限是否仍然有效（用户可能在系统设置里撤销了）
            val permValid = hasDir && run {
                val perms = contentResolver.persistedUriPermissions
                perms.any { it.uri.toString() == cfg.treeUri && it.isReadPermission }
            }
            val sb = StringBuilder("{")
            sb.append("\"hasDir\":").append(permValid)
            sb.append(",\"dirDisplay\":\"").append(escJSON(dir)).append("\"")
            sb.append(",\"remoteBase\":\"").append(escJSON(cfg.remoteBase)).append("\"")
            sb.append(",\"autoBackup\":").append(cfg.autoBackup)
            sb.append(",\"lastBackupTime\":").append(cfg.lastBackupTime)
            sb.append("}")
            return sb.toString()
        }

        /** 修改远程备份根路径，持久化到 DataStore。 */
        @JavascriptInterface
        fun setRemoteBase(base: String) {
            kotlinx.coroutines.runBlocking { backupStore.setRemoteBase(base) }
        }

        /** 开启/关闭自动备份。开启时注册 WorkManager 定时任务（充电+Wi-Fi 约束）。 */
        @JavascriptInterface
        fun setAutoBackup(on: Boolean) {
            kotlinx.coroutines.runBlocking { backupStore.setAutoBackup(on) }
            if (on) BackupWorker.enable(this@MainActivity)
            else BackupWorker.disable(this@MainActivity)
        }

        /** App 内查看图片：启动 ImageActivity 全屏 WebView 加载（注入 cookie）。
         *  仅 portal.html 里 viewImage() 在 ddnas 桥可用时调用。 */
        @JavascriptInterface
        fun viewImage(url: String, name: String) {
            runOnUiThread { startImage(url, name) }
        }

        /** App 内下载文件：弹确认对话框，确认后用 DownloadManager 写入公共 Download 目录，
         *  注入 admin cookie 解决 stream 鉴权。 */
        @JavascriptInterface
        fun downloadFile(url: String, name: String) {
            runOnUiThread { showDownloadDialog(url, name) }
        }

        /** 把 portal 前端的诊断信息写入 Logcat（tag=DDNAS-Portal），
         *  方便排查 WebView 内 fetch 失败、超时等问题。仅 App 端可用。 */
        @JavascriptInterface
        fun log(msg: String) {
            android.util.Log.i("DDNAS-Portal", msg ?: "")
        }

        private fun escJSON(s: String): String =
            s.replace("\\", "\\\\").replace("\"", "\\\"")
                .replace("\n", "\\n").replace("\r", "\\r")
    }

    /** 通知权限授予后待执行的备份动作标记。 */
    private var pendingBackupAfterPerm = false

    private fun startPlayer(streamUrl: String) {
        val active = currentServer() ?: return
        val cookie = CookieManager.getInstance().getCookie(active.url)
        android.util.Log.i("DDNAS-Main", "startPlayer url=$streamUrl cookieLen=${cookie?.length ?: 0}")
        if (cookie.isNullOrEmpty()) {
            android.widget.Toast.makeText(this, "未获取到登录会话，请先在页面登录", android.widget.Toast.LENGTH_LONG).show()
        }
        startActivity(
            Intent(this, PlayerActivity::class.java).apply {
                putExtra(PlayerActivity.EXTRA_URL, streamUrl)
                putExtra(PlayerActivity.EXTRA_HOST, active.url)
                putExtra(PlayerActivity.EXTRA_COOKIE, cookie ?: "")
            }
        )
    }

    /** 启动图片预览 Activity。cookie 来自 WebView 当前会话。 */
    private fun startImage(imageUrl: String, name: String) {
        val active = currentServer() ?: return
        val cookie = CookieManager.getInstance().getCookie(active.url) ?: ""
        startActivity(
            Intent(this, ImageActivity::class.java).apply {
                putExtra(ImageActivity.EXTRA_URL, imageUrl)
                putExtra(ImageActivity.EXTRA_COOKIE, cookie)
            }
        )
    }

    /** 下载确认对话框：用户确认后用 DownloadManager 写入公共 Download 目录。 */
    private fun showDownloadDialog(url: String, name: String) {
        android.app.AlertDialog.Builder(this)
            .setTitle("下载文件")
            .setMessage("将下载到手机 Download 目录：\n$name")
            .setPositiveButton("下载") { _, _ ->
                startDownloadWithManager(url, name)
            }
            .setNegativeButton("取消", null)
            .show()
    }

    /** 用 DownloadManager 异步下载，注入 cookie 解决 stream 鉴权。 */
    private fun startDownloadWithManager(url: String, name: String) {
        val active = currentServer()
        if (active == null) {
            android.widget.Toast.makeText(this, "未配置服务器", android.widget.Toast.LENGTH_SHORT).show()
            return
        }
        val cookie = CookieManager.getInstance().getCookie(active.url) ?: ""
        val dm = getSystemService(android.content.Context.DOWNLOAD_SERVICE) as android.app.DownloadManager
        val safeName = name.ifBlank { "ddnas_file" }
            .replace("/", "_").replace("\\", "_").replace(":", "_")
        val req = android.app.DownloadManager.Request(Uri.parse(url)).apply {
            setTitle("DDNAS: $safeName")
            setDescription("从中间件下载到 Download 目录")
            setNotificationVisibility(android.app.DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED)
            // 公共 Download 目录，Android 10+ 无需写权限
            setDestinationInExternalPublicDir(android.os.Environment.DIRECTORY_DOWNLOADS, safeName)
            if (cookie.isNotEmpty()) addRequestHeader("Cookie", cookie)
            addRequestHeader("User-Agent", "DDNAS-App")
        }
        try {
            dm.enqueue(req)
            android.widget.Toast.makeText(this, "已开始下载: $safeName", android.widget.Toast.LENGTH_SHORT).show()
        } catch (e: Exception) {
            android.util.Log.e("DDNAS-Download", "enqueue fail", e)
            android.widget.Toast.makeText(this, "下载失败: ${e.message}", android.widget.Toast.LENGTH_LONG).show()
        }
    }

    private fun onTreePicked(treeUri: Uri) {
        val active = currentServer() ?: return
        try {
            contentResolver.takePersistableUriPermission(
                treeUri, Intent.FLAG_GRANT_READ_URI_PERMISSION
            )
        } catch (_: SecurityException) {
        }
        // 持久化 treeUri，后续备份无需重选目录
        val treeUriStr = treeUri.toString()
        kotlinx.coroutines.runBlocking { backupStore.setTreeUri(treeUriStr) }
        val cfg = kotlinx.coroutines.runBlocking { backupStore.get() }
        startBackupService(treeUriStr, cfg.remoteBase)
    }

    /** 读取已选 SAF 目录的简短可读路径（content uri 的 last path segment）。 */
    private fun treeUriDisplay(uriStr: String): String {
        if (uriStr.isEmpty()) return ""
        return try {
            val uri = Uri.parse(uriStr)
            // tree uri 形如 content://com.android.externalstorage.documents/tree/primary%3ADCIM
            val seg = uri.lastPathSegment ?: uri.path ?: uriStr
            java.net.URLDecoder.decode(seg, "UTF-8")
                .substringAfter("tree/").ifEmpty { seg }
        } catch (_: Exception) { uriStr }
    }

    /** 启动备份前台服务，传入 treeUri/远程路径/cookie。
     *  若已有备份在跑则不重复启动（前端按钮已切换为"取消"，此处兜底防并发）。 */
    private fun startBackupService(treeUriStr: String, remoteBase: String) {
        if (BackupService.isRunning()) return
        val active = currentServer() ?: return
        val cookie = CookieManager.getInstance().getCookie(active.url) ?: ""
        androidx.core.content.ContextCompat.startForegroundService(
            this,
            Intent(this, BackupService::class.java).apply {
                putExtra(BackupService.EXTRA_TREE_URI, treeUriStr)
                putExtra(BackupService.EXTRA_ORIGIN, active.url.trimEnd('/'))
                putExtra(BackupService.EXTRA_COOKIE, cookie)
                putExtra(BackupService.EXTRA_REMOTE_BASE, remoteBase)
            }
        )
    }

    /** 阻塞读取当前选中服务器（JS 桥/回调里无协程上下文时用）。 */
    private fun currentServer(): Server? {
        val servers = kotlinx.coroutines.runBlocking { store.servers.first() }
        val idx = kotlinx.coroutines.runBlocking { store.activeIndex.first() }
        return servers.getOrNull(idx)
    }
}
