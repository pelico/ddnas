package io.github.pelico.ddnas.ui.player

import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.viewinterop.AndroidView
import androidx.media3.common.MediaItem
import androidx.media3.datasource.okhttp.OkHttpDataSource
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.DefaultMediaSourceFactory
import androidx.media3.ui.PlayerView
import io.github.pelico.ddnas.DdnasApplication
import io.github.pelico.ddnas.net.RetrofitProvider

/**
 * Lightweight transport for the URL the player should stream. Navigation-arg
 * encoding of a full URL (with slashes / colons) is fragile, so the files
 * screen drops the absolute stream URL here right before navigating.
 */
object PlayerLauncher {
    @Volatile
    var pendingStreamUrl: String = ""
}

/**
 * Streams a media file from `/api/openlist/files/stream/...`. The Bearer token
 * is attached via the OkHttp data source so Range requests stay authenticated.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun PlayerScreen(
    streamUrl: String,
    onBack: () -> Unit
) {
    val context = LocalContext.current
    val app = context.applicationContext as DdnasApplication
    val token by app.settings.appToken.collectAsState(initialValue = "")

    val exoPlayer = remember(streamUrl, token) {
        val okFactory = OkHttpDataSource.Factory(RetrofitProvider.okHttpClient)
            .setDefaultRequestProperties(mapOf("Authorization" to "Bearer $token"))
        ExoPlayer.Builder(context)
            .setMediaSourceFactory(
                DefaultMediaSourceFactory(context).setDataSourceFactory(okFactory)
            )
            .build()
            .also { player ->
                player.setMediaItem(MediaItem.fromUri(streamUrl))
                player.prepare()
                player.playWhenReady = true
            }
    }

    DisposableEffect(exoPlayer) {
        onDispose { exoPlayer.release() }
    }

    Scaffold(
        modifier = Modifier.fillMaxSize(),
        topBar = {
            TopAppBar(
                title = { Text("播放") },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "返回")
                    }
                }
            )
        }
    ) { innerPadding ->
        AndroidView(
            modifier = Modifier
                .fillMaxSize()
                .padding(innerPadding),
            factory = { ctx ->
                PlayerView(ctx).apply {
                    player = exoPlayer
                    useController = true
                }
            }
        )
    }
}
