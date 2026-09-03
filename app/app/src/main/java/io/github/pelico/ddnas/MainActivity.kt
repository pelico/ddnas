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

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        treePicker = registerForActivityResult(ActivityResultContracts.OpenDocumentTree()) { uri ->
            if (uri != null) onTreePicked(uri)
        }
        notifPermission = registerForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
            if (granted) treePicker.launch(null)
        }
        // <input type="file"> 选择回调，portal 上传按钮需要
        fileChooser = registerForActivityResult(ActivityResultContracts.GetContent()) { uri ->
            filePathCallback?.onReceiveValue(if (uri != null) arrayOf(uri) else null)
            filePathCallback = null
        }

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
                    PortalWebView(server = active)
                }
            }
            BackupProgressDialog(backup)
        }
    }

    /** 加载 /portal 的 WebView，注入 ddnas 桥。按服务器 url+name 为 key 重建以切换。 */
    @Composable
    private fun PortalWebView(server: Server) {
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
                        webViewClient = WebViewClient()
                        // 让 portal 的 <input type="file"> 上传按钮可用
                        webChromeClient = object : WebChromeClient() {
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
                        CookieManager.getInstance().setAcceptCookie(true)
                        CookieManager.getInstance().setAcceptThirdPartyCookies(this, true)
                        addJavascriptInterface(Bridge(), "ddnas")
                        loadUrl(server.url.trimEnd('/') + "/portal")
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

    @Composable
    private fun BackupProgressDialog(progress: BackupService.Progress) {
        val msg = when (progress) {
            is BackupService.Progress.Running -> "备份中：${progress.done}/${progress.total}\n${progress.current}"
            is BackupService.Progress.Done -> progress.message
            is BackupService.Progress.Error -> "备份出错：${progress.message}"
            BackupService.Progress.Scanning -> "扫描文件中…"
            BackupService.Progress.Idle -> return
        }
        AlertDialog(
            onDismissRequest = { BackupService.reset() },
            confirmButton = { TextButton(onClick = { BackupService.reset() }) { Text("关闭") } },
            title = { Text("备份") },
            text = { Text(msg) }
        )
    }

    // --- JS 桥 ---

    /** 暴露给 /portal 页面的原生桥：ddnas.playMedia(url) / ddnas.startBackup()。 */
    private inner class Bridge {
        @JavascriptInterface
        fun playMedia(url: String) {
            runOnUiThread { startPlayer(url) }
        }

        @JavascriptInterface
        fun startBackup() {
            runOnUiThread {
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU &&
                    checkSelfPermission(android.Manifest.permission.POST_NOTIFICATIONS) !=
                    android.content.pm.PackageManager.PERMISSION_GRANTED
                ) {
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
        }
    }

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

    /** 启动备份前台服务，传入 treeUri/远程路径/cookie。 */
    private fun startBackupService(treeUriStr: String, remoteBase: String) {
        val active = currentServer() ?: return
        val cookie = CookieManager.getInstance().getCookie(active.url) ?: ""
        BackupService.reset()
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
