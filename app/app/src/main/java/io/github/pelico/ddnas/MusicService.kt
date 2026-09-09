package io.github.pelico.ddnas

import android.app.Notification
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.media.AudioAttributes
import android.media.AudioFocusRequest
import android.media.AudioManager
import android.net.Uri
import android.net.wifi.WifiManager
import android.os.Build
import android.os.IBinder
import android.os.PowerManager
import android.support.v4.media.MediaMetadataCompat
import android.support.v4.media.session.MediaSessionCompat
import android.support.v4.media.session.PlaybackStateCompat
import android.util.Log
import androidx.core.app.NotificationCompat
import androidx.media3.common.MediaItem
import androidx.media3.common.Player
import androidx.media3.datasource.DataSource
import androidx.media3.datasource.okhttp.OkHttpDataSource
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.DefaultMediaSourceFactory
import okhttp3.OkHttpClient

/**
 * 音乐后台播放服务：前台服务 + ExoPlayer，息屏/切后台仍连续播放。
 *
 * 设计要点（参考后台播放经验）：
 * - 播放状态机、队列推进（下一首）放在 Service，不依赖 Activity 生命周期
 * - WakeLock/WifiLock 在播放开始 acquire，真正停止（用户 stop/列表结束）才 release；
 *   "播完→自动下一首"间隙持锁不释放，避免 Doze 延迟断播
 * - ExoPlayer REPEAT_MODE_ALL 实现列表循环
 * - 前台通知 + MediaSession 支持锁屏控制
 * - 通过 [stateCallback] 把状态回传给 WebView（MainActivity 设置回调，evaluateJavascript）
 */
class MusicService : Service() {

    private var player: ExoPlayer? = null
    private var mediaSession: MediaSessionCompat? = null
    private var wakeLock: PowerManager.WakeLock? = null
    private var wifiLock: WifiManager.WifiLock? = null
    private var audioFocusRequest: AudioFocusRequest? = null
    private var audioManager: AudioManager? = null

    private var playlist: List<Track> = emptyList()
    private var currentIndex = 0
    private var host = ""
    private var cookie = ""

    // 连续播放错误计数：防 onPlayerError→nextTrack 死循环导致 ANR/闪退。
    // cookie 过期时所有 track 都 401，REPEAT_MODE_ALL 会无限循环跳下一首，
    // 每次错误在主线程同步触发 onPlayerError+notifyState，过载→系统杀进程。
    // 连续超 MAX_CONSECUTIVE_ERRORS 次后停止自动跳转，只通知 UI。
    private var consecutiveErrors = 0
    private val MAX_CONSECUTIVE_ERRORS = 3

    // 播放进度定时推送：等价于 Web 端 Audio.timeupdate 事件
    // 节能退避：UI 可见时 1s 推（进度条流畅）；App 切后台时逐级退避到 5s→10s→30s，
    // 后台仍保留推送（非完全停）以便切歌时 UI 能在 ≤30s 内同步，且不依赖前台。
    // 播放连续性靠 WakeLock+WifiLock+ExoPlayer 缓冲，与进度推送频率无关，退避零影响。
    private val progressHandler = android.os.Handler(android.os.Looper.getMainLooper())
    private var uiVisible = true  // 由 MainActivity onStart/onStop 通过桥设置
    private var bgBackoffIdx = 0  // 后台退避阶梯索引
    private val progressRunnable = object : Runnable {
        override fun run() {
            if (player?.isPlaying == true) {
                notifyState()
                // 后台逐级退避 5s→10s→30s（封顶 30s）；前台恢复 1s
                val delay = if (uiVisible) {
                    bgBackoffIdx = 0
                    1000L
                } else {
                    val steps = longArrayOf(5000, 10000, 30000)
                    val d = steps[minOf(bgBackoffIdx, steps.size - 1)]
                    bgBackoffIdx = minOf(bgBackoffIdx + 1, steps.size - 1)
                    d
                }
                progressHandler.postDelayed(this, delay)
            }
        }
    }

    // 睡眠定时器：到点暂停播放（不 stop，保留播放位置，用户醒了可继续）。
    // 用同一个 progressHandler（主线程 Looper），与进度推送共用线程，无需额外 Handler。
    private var sleepRunnable: Runnable? = null
    private var sleepEndTime: Long = 0  // 0=未设定；>0=到点时间戳（System.currentTimeMillis）

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onCreate() {
        super.onCreate()
        audioManager = getSystemService(Context.AUDIO_SERVICE) as AudioManager
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_PLAY -> resumePlayer()
            ACTION_PAUSE -> pausePlayer()
            ACTION_NEXT -> nextTrack()
            ACTION_PREV -> prevTrack()
            ACTION_STOP -> stopMusic()
        }
        return START_STICKY
    }

    // ---------- 公共控制（由 MainActivity JS 桥调用） ----------

    /** 由 MainActivity onStart/onStop 调用：标记 UI 可见性，驱动进度推送退避节能 */
    fun setUiVisible(visible: Boolean) {
        uiVisible = visible
        if (visible) {
            bgBackoffIdx = 0
            // 回前台若正在播放且进度推送已停（如暂停过），立即补推一次
            if (player?.isPlaying == true) {
                progressHandler.removeCallbacks(progressRunnable)
                progressHandler.post(progressRunnable)
            }
        }
    }

    fun play(index: Int, tracks: List<Track>, host: String, cookie: String) {
        this.playlist = tracks
        this.currentIndex = index
        this.host = host
        this.cookie = cookie
        startForeground(NOTIF_ID, buildNotification(tracks.getOrNull(index)?.name ?: "播放"))
        acquireLocks()
        requestAudioFocus()
        initPlayerIfNeeded()
        val items = tracks.map { MediaItem.fromUri(it.url) }
        player?.apply {
            setMediaItems(items, index, 0)
            prepare()
            playWhenReady = true
        }
        notifyState()
    }

    fun playAt(index: Int, cookie: String = "") {
        if (index !in playlist.indices) return
        // 刷新 cookie：App 后台一段时间后 NAS 会话可能过期，旧 cookie 401 导致加载失败
        if (cookie.isNotEmpty()) refreshCookie(cookie)
        currentIndex = index
        player?.seekTo(index, 0)
        player?.playWhenReady = true
        // 重置连续错误计数：用户主动切歌视为新一轮尝试
        consecutiveErrors = 0
        updateNotification(playlist[index].name)
        notifyState()
    }

    fun pausePlayer() {
        player?.playWhenReady = false
        notifyState()
    }

    fun resumePlayer() {
        player?.playWhenReady = true
        notifyState()
    }

    fun nextTrack() {
        player?.seekToNext()
        notifyState()
    }

    fun prevTrack() {
        player?.seekToPrevious()
        notifyState()
    }

    /** percent: 0~100 */
    fun seekToPercent(percent: Int) {
        val p = player ?: return
        val dur = p.duration
        if (dur > 0) p.seekTo((dur * percent / 100).toLong())
    }

    /**
     * 睡眠定时器：[minutes] 分钟后暂停播放。
     * - 暂停而非停止：保留播放位置，用户醒了可点播放继续
     * - Service 持有 WakeLock，即使屏幕熄灭/Doze 到点也一定执行
     * - 设定后通过 notifyState() 通知 UI 显示倒计时
     */
    fun setSleepTimer(minutes: Int) {
        cancelSleepTimer()
        if (minutes <= 0) return
        val r = Runnable {
            pausePlayer()
            sleepRunnable = null
            sleepEndTime = 0
            notifyState()
        }
        sleepRunnable = r
        sleepEndTime = System.currentTimeMillis() + minutes * 60_000L
        progressHandler.postDelayed(r, minutes * 60_000L)
        notifyState()
    }

    fun cancelSleepTimer() {
        sleepRunnable?.let { progressHandler.removeCallbacks(it) }
        sleepRunnable = null
        sleepEndTime = 0
        notifyState()
    }

    fun stopMusic() {
        progressHandler.removeCallbacks(progressRunnable)
        cancelSleepTimer()
        player?.stop()
        releaseLocks()
        abandonAudioFocus()
        stopForeground(STOP_FOREGROUND_REMOVE)
        stopSelf()
        notifyState()
    }

    fun getStateJson(): String {
        val p = player
        val playing = p?.isPlaying == true
        val pos = p?.currentPosition ?: 0
        val dur = p?.duration ?: 0
        val idx = p?.currentMediaItemIndex ?: currentIndex
        val sleepMs = if (sleepEndTime > 0) (sleepEndTime - System.currentTimeMillis()).coerceAtLeast(0) else 0
        return """{"playing":$playing,"index":$idx,"position":$pos,"duration":$dur,"sleepMs":$sleepMs}"""
    }

    // ---------- 内部 ----------

    private fun initPlayerIfNeeded() {
        if (player != null) return
        val client = buildAuthedClient()
        val factory: DataSource.Factory = OkHttpDataSource.Factory(client)
        val mediaSourceFactory = DefaultMediaSourceFactory(this).setDataSourceFactory(factory)
        player = ExoPlayer.Builder(this)
            .setMediaSourceFactory(mediaSourceFactory)
            .build()
            .also { p ->
                p.repeatMode = Player.REPEAT_MODE_ALL
                p.addListener(object : Player.Listener {
                    override fun onIsPlayingChanged(isPlaying: Boolean) {
                        notifyState()
                        updateNotification(playlist.getOrNull(p.currentMediaItemIndex)?.name ?: "")
                        // 播放中启动定时进度推送，暂停/停止时取消
                        progressHandler.removeCallbacks(progressRunnable)
                        if (isPlaying) {
                            consecutiveErrors = 0  // 成功播放，重置错误计数
                            progressHandler.postDelayed(progressRunnable, 1000)
                        }
                    }
                    override fun onMediaItemTransition(mediaItem: MediaItem?, reason: Int) {
                        currentIndex = p.currentMediaItemIndex
                        notifyState()
                        updateNotification(playlist.getOrNull(currentIndex)?.name ?: "")
                    }
                    override fun onPlayerError(error: androidx.media3.common.PlaybackException) {
                        Log.e(TAG, "player error (consecutive=${consecutiveErrors})", error)
                        consecutiveErrors++
                        if (consecutiveErrors > MAX_CONSECUTIVE_ERRORS) {
                            // 连续错误超限：cookie 过期/网络断开导致所有 track 都失败，
                            // 不再自动跳下一首（避免死循环 ANR），停止播放并通知 UI
                            Log.w(TAG, "max consecutive errors reached, stopping auto-retry")
                            consecutiveErrors = 0
                            player?.playWhenReady = false
                            notifyState()
                        } else {
                            // 单次错误（偶发网络抖动）：跳下一首重试
                            nextTrack()
                        }
                    }
                })
            }
        initMediaSession()
    }

    private fun buildAuthedClient(): OkHttpClient {
        val base = (application as DdnasApplication).okHttpClient
        // 拦截器读 Service 的可变 cookie/host 字段（非闭包捕获），
        // 这样 playAt/refreshCookie 更新 cookie 后，下次请求自动用新值，
        // 不需要重建 ExoPlayer（DataSourceFactory 绑定在 player 上无法热替换）。
        return base.newBuilder()
            .addInterceptor { chain ->
                val req = chain.request()
                val target = req.url.host
                val originHost = Uri.parse(this@MusicService.host)?.host ?: this@MusicService.host
                val ck = this@MusicService.cookie
                if (target == originHost && ck.isNotEmpty()) {
                    chain.proceed(req.newBuilder().header("Cookie", ck).build())
                } else {
                    chain.proceed(req)
                }
            }
            .build()
    }

    /** 刷新会话 cookie（由 playAt 在切换曲目时从 JS 桥传入，防止后台过期） */
    fun refreshCookie(cookie: String) {
        if (cookie.isNotEmpty()) this.cookie = cookie
    }

    private fun initMediaSession() {
        mediaSession = MediaSessionCompat(this, TAG).apply {
            setCallback(object : MediaSessionCompat.Callback() {
                override fun onPlay() { resumePlayer() }
                override fun onPause() { pausePlayer() }
                override fun onSkipToNext() { nextTrack() }
                override fun onSkipToPrevious() { prevTrack() }
                override fun onStop() { stopMusic() }
                override fun onSeekTo(pos: Long) { player?.seekTo(pos) }
            })
            isActive = true
        }
    }

    private fun acquireLocks() {
        if (wakeLock == null) {
            wakeLock = (getSystemService(Context.POWER_SERVICE) as PowerManager)
                .newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "ddnas:music").apply {
                    setReferenceCounted(false)
                    acquire()
                }
        }
        if (wifiLock == null) {
            wifiLock = (applicationContext.getSystemService(Context.WIFI_SERVICE) as WifiManager)
                .createWifiLock(WifiManager.WIFI_MODE_FULL_HIGH_PERF, "ddnas:music").apply {
                    setReferenceCounted(false)
                    acquire()
                }
        }
    }

    private fun releaseLocks() {
        wakeLock?.takeIf { it.isHeld }?.release()
        wifiLock?.takeIf { it.isHeld }?.release()
        wakeLock = null
        wifiLock = null
    }

    private fun requestAudioFocus() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            audioFocusRequest = AudioFocusRequest.Builder(AudioManager.AUDIOFOCUS_GAIN).apply {
                setAudioAttributes(
                    AudioAttributes.Builder()
                        .setUsage(AudioAttributes.USAGE_MEDIA)
                        .setContentType(AudioAttributes.CONTENT_TYPE_MUSIC)
                        .build()
                )
                setOnAudioFocusChangeListener { focusChange ->
                    when (focusChange) {
                        AudioManager.AUDIOFOCUS_LOSS -> stopMusic()
                        AudioManager.AUDIOFOCUS_LOSS_TRANSIENT -> pausePlayer()
                        AudioManager.AUDIOFOCUS_GAIN -> resumePlayer()
                    }
                }
            }.build()
            audioManager?.requestAudioFocus(audioFocusRequest!!)
        } else {
            @Suppress("DEPRECATION")
            audioManager?.requestAudioFocus({ }, AudioManager.STREAM_MUSIC, AudioManager.AUDIOFOCUS_GAIN)
        }
    }

    private fun abandonAudioFocus() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            audioFocusRequest?.let { audioManager?.abandonAudioFocusRequest(it) }
        } else {
            @Suppress("DEPRECATION")
            audioManager?.abandonAudioFocus { }
        }
        audioFocusRequest = null
    }

    private fun buildNotification(title: String): Notification {
        val ctx = this
        val playPauseAction = if (player?.isPlaying == true) {
            NotificationCompat.Action(0, "暂停", pendingIntent(ACTION_PAUSE))
        } else {
            NotificationCompat.Action(0, "播放", pendingIntent(ACTION_PLAY))
        }
        return NotificationCompat.Builder(ctx, DdnasApplication.CHANNEL_MUSIC)
            .setSmallIcon(android.R.drawable.ic_media_play)
            .setContentTitle(title)
            .setContentText("DDNAS 音乐")
            .setOngoing(true)
            .addAction(NotificationCompat.Action(0, "上一首", pendingIntent(ACTION_PREV)))
            .addAction(playPauseAction)
            .addAction(NotificationCompat.Action(0, "下一首", pendingIntent(ACTION_NEXT)))
            .addAction(NotificationCompat.Action(0, "关闭", pendingIntent(ACTION_STOP)))
            .setStyle(
                androidx.media.app.NotificationCompat.MediaStyle()
                    .setMediaSession(mediaSession?.sessionToken)
                    .setShowActionsInCompactView(1, 2)
            )
            .build()
    }

    private fun updateNotification(title: String) {
        val nm = getSystemService(Context.NOTIFICATION_SERVICE) as android.app.NotificationManager
        nm.notify(NOTIF_ID, buildNotification(title))
    }

    private fun pendingIntent(action: String): PendingIntent {
        val intent = Intent(this, MusicService::class.java).setAction(action)
        val flags = PendingIntent.FLAG_UPDATE_CURRENT or
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M) PendingIntent.FLAG_IMMUTABLE else 0
        return PendingIntent.getService(this, action.hashCode(), intent, flags)
    }

    // stateCallback 防抖：错误循环时 onPlayerError 会高频调 notifyState→evaluateJavascript，
    // 连续推送淹没 WebView 主线程。合并 150ms 内的多次通知为一次，保 UI 不卡。
    private val notifyRunnable = Runnable {
        val cb = stateCallback ?: return@Runnable
        cb.invoke(getStateJson())
    }
    private fun notifyState() {
        progressHandler.removeCallbacks(notifyRunnable)
        progressHandler.post(notifyRunnable)
    }

    override fun onDestroy() {
        super.onDestroy()
        progressHandler.removeCallbacks(progressRunnable)
        progressHandler.removeCallbacks(notifyRunnable)
        sleepRunnable?.let { progressHandler.removeCallbacks(it) }
        sleepRunnable = null
        player?.release()
        player = null
        mediaSession?.release()
        mediaSession = null
        releaseLocks()
        abandonAudioFocus()
    }

    data class Track(val name: String, val url: String)

    companion object {
        private const val TAG = "DDNAS-Music"
        private const val NOTIF_ID = 4243

        const val ACTION_PLAY = "io.github.pelico.ddnas.music.PLAY"
        const val ACTION_PAUSE = "io.github.pelico.ddnas.music.PAUSE"
        const val ACTION_NEXT = "io.github.pelico.ddnas.music.NEXT"
        const val ACTION_PREV = "io.github.pelico.ddnas.music.PREV"
        const val ACTION_STOP = "io.github.pelico.ddnas.music.STOP"

        /** 当前服务实例（由 onStartCommand 后设置），供 JS 桥直接调用控制方法。 */
        var instance: MusicService? = null
            private set

        /** 状态回调：MainActivity 设置，Service 状态变化时调用（JSON 字符串）。 */
        var stateCallback: ((String) -> Unit)? = null

        /** JS 桥调用：启动播放。若服务未运行则先 startForegroundService。 */
        fun play(context: Context, index: Int, tracks: List<Track>, host: String, cookie: String) {
            val intent = Intent(context, MusicService::class.java)
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
                context.startForegroundService(intent)
            } else {
                context.startService(intent)
            }
            // 等 Service onCreate 后调用 play（Service 启动是异步的，这里用 post 轮询）
            android.os.Handler(context.mainLooper).post(object : Runnable {
                override fun run() {
                    val svc = instance
                    if (svc != null) {
                        svc.play(index, tracks, host, cookie)
                    } else {
                        android.os.Handler(context.mainLooper).postDelayed(this, 50)
                    }
                }
            })
        }
    }

    override fun onTaskRemoved(rootIntent: Intent?) {
        // 用户从最近任务划掉 App 时停止播放（避免僵尸服务）
        stopMusic()
        super.onTaskRemoved(rootIntent)
    }

    init {
        instance = this
    }
}