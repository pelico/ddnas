package io.github.pelico.ddnas.ui.files

import android.provider.OpenableColumns
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Folder
import androidx.compose.material.icons.filled.InsertDriveFile
import androidx.compose.material.icons.filled.Upload
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import io.github.pelico.ddnas.data.model.FileItem
import io.github.pelico.ddnas.ui.common.formatBytes
import io.github.pelico.ddnas.ui.common.isMediaFile
import io.github.pelico.ddnas.ui.player.PlayerLauncher

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun FilesScreen(
    contentPadding: PaddingValues,
    onPlay: () -> Unit,
    viewModel: FilesViewModel = viewModel()
) {
    val context = LocalContext.current
    val path by viewModel.path.collectAsStateWithLifecycle()
    val state by viewModel.state.collectAsStateWithLifecycle()
    val uploadState by viewModel.uploadState.collectAsStateWithLifecycle()
    val snackbarHostState = remember { SnackbarHostState() }

    val pickFile = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri ->
        if (uri != null) {
            val resolver = context.contentResolver
            var name = "file"
            var size = -1L
            resolver.query(uri, null, null, null, null)?.use { cursor ->
                val nameIdx = cursor.getColumnIndex(OpenableColumns.DISPLAY_NAME)
                val sizeIdx = cursor.getColumnIndex(OpenableColumns.SIZE)
                if (cursor.moveToFirst()) {
                    if (nameIdx >= 0) name = cursor.getString(nameIdx)
                    if (sizeIdx >= 0) size = cursor.getLong(sizeIdx)
                }
            }
            val finalName = name
            val finalSize = size
            viewModel.upload(finalName, { resolver.openInputStream(uri)!! }, finalSize)
        }
    }

    LaunchedEffect(uploadState) {
        when (val u = uploadState) {
            is UploadState.Success -> { snackbarHostState.showSnackbar(u.message); viewModel.clearUploadState() }
            is UploadState.Error -> { snackbarHostState.showSnackbar(u.message); viewModel.clearUploadState() }
            else -> Unit
        }
    }

    LaunchedEffect(Unit) {
        if (state is FilesState.Idle) viewModel.load()
    }

    Scaffold(
        modifier = Modifier.fillMaxSize(),
        topBar = {
            TopAppBar(
                title = { Text(path, maxLines = 1, overflow = TextOverflow.Ellipsis) },
                navigationIcon = {
                    if (path != "/") {
                        IconButton(onClick = viewModel::goUp) {
                            Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "上级")
                        }
                    }
                },
                actions = {
                    IconButton(onClick = { pickFile.launch(arrayOf("*/*")) }) {
                        Icon(Icons.Filled.Upload, contentDescription = "上传到当前目录")
                    }
                }
            )
        },
        snackbarHost = { SnackbarHost(snackbarHostState) }
    ) { innerPadding ->
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(innerPadding)
                .padding(contentPadding)
        ) {
            Breadcrumb(path = path, onSegment = viewModel::navigateTo)

            when (val current = state) {
                is FilesState.Loading, FilesState.Idle -> Box(
                    modifier = Modifier.fillMaxSize(),
                    contentAlignment = Alignment.Center
                ) { CircularProgressIndicator() }

                is FilesState.Error -> Box(
                    modifier = Modifier.fillMaxSize(),
                    contentAlignment = Alignment.Center
                ) {
                    Column(horizontalAlignment = Alignment.CenterHorizontally) {
                        Text(current.message, color = MaterialTheme.colorScheme.error)
                        TextButton(onClick = { viewModel.load() }) { Text("重试") }
                    }
                }

                is FilesState.Success -> {
                    if (current.items.isEmpty()) {
                        Box(modifier = Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                            Text("空目录", color = MaterialTheme.colorScheme.onSurfaceVariant)
                        }
                    } else {
                        LazyColumn(modifier = Modifier.fillMaxSize()) {
                            items(current.items, key = { it.name + it.is_dir }) { item ->
                                FileRow(item) {
                                    if (item.is_dir) {
                                        viewModel.openDirectory(item.name)
                                    } else if (isMediaFile(item.name)) {
                                        PlayerLauncher.pendingStreamUrl =
                                            viewModel.streamUrlFor(item.name)
                                        onPlay()
                                    }
                                }
                                HorizontalDivider()
                            }
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun Breadcrumb(path: String, onSegment: (String) -> Unit) {
    val segments = buildList {
        add("/" to "/")
        if (path.isNotBlank() && path != "/") {
            val parts = path.trim('/').split('/')
            var acc = ""
            parts.forEach { part ->
                acc = if (acc.isEmpty()) "/$part" else "$acc/$part"
                add(part to acc)
            }
        }
    }
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = 16.dp, vertical = 8.dp),
        horizontalArrangement = Arrangement.spacedBy(8.dp)
    ) {
        segments.forEachIndexed { index, (label, target) ->
            TextButton(onClick = { onSegment(target) }) {
                Text(
                    if (index == 0) "根" else label,
                    color = if (index == segments.lastIndex) MaterialTheme.colorScheme.primary
                    else MaterialTheme.colorScheme.onSurfaceVariant
                )
            }
            if (index != segments.lastIndex) {
                Text("/", color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.align(Alignment.CenterVertically))
            }
        }
    }
}

@Composable
private fun FileRow(item: FileItem, onClick: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
            .padding(horizontal = 16.dp, vertical = 12.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(12.dp)
    ) {
        Icon(
            if (item.is_dir) Icons.Filled.Folder else Icons.Filled.InsertDriveFile,
            contentDescription = null,
            tint = MaterialTheme.colorScheme.primary
        )
        Column(modifier = Modifier.weight(1f)) {
            Text(item.name, maxLines = 1, overflow = TextOverflow.Ellipsis)
            val subtitle = if (item.is_dir) item.modified else "${formatBytes(item.size)} · ${item.modified}"
            Text(
                subtitle,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant
            )
        }
    }
}
