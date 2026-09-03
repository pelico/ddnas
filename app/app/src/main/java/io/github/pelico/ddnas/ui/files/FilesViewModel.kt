package io.github.pelico.ddnas.ui.files

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import io.github.pelico.ddnas.DdnasApplication
import io.github.pelico.ddnas.data.model.FileItem
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import java.io.InputStream

class FilesViewModel(app: Application) : AndroidViewModel(app) {

    private val settings = getApplication<DdnasApplication>().settings
    private val repository = getApplication<DdnasApplication>().repository

    private val serverUrl: StateFlow<String> = settings.serverUrl
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5000), "")

    private val _path = MutableStateFlow("/")
    val path: StateFlow<String> = _path.asStateFlow()

    private val _state = MutableStateFlow<FilesState>(FilesState.Idle)
    val state: StateFlow<FilesState> = _state.asStateFlow()

    private val _uploadState = MutableStateFlow<UploadState>(UploadState.Idle)
    val uploadState: StateFlow<UploadState> = _uploadState.asStateFlow()

    val baseUrl: String get() = serverUrl.value

    fun load() {
        val base = serverUrl.value
        if (base.isBlank()) {
            _state.value = FilesState.Error("未配置服务器地址")
            return
        }
        _state.value = FilesState.Loading
        viewModelScope.launch {
            _state.value = try {
                val resp = repository.listFiles(base, _path.value)
                FilesState.Success(resp.items, resp.total)
            } catch (e: Exception) {
                FilesState.Error(e.message ?: "加载文件列表失败")
            }
        }
    }

    fun openDirectory(name: String) {
        val parent = _path.value.trimEnd('/')
        _path.value = if (parent.isEmpty()) "/$name" else "$parent/$name"
        load()
    }

    fun navigateTo(path: String) {
        _path.value = if (path.isBlank()) "/" else path
        load()
    }

    fun goUp() {
        val current = _path.value.trimEnd('/')
        if (current.isEmpty() || current == "/") return
        val idx = current.lastIndexOf('/')
        _path.value = if (idx <= 0) "/" else current.substring(0, idx)
        load()
    }

    fun streamUrlFor(name: String): String {
        val parent = _path.value.trimEnd('/')
        val dir = if (parent.isEmpty()) "" else parent
        return repository.streamUrl(serverUrl.value, "$dir/$name")
    }

    fun upload(name: String, content: () -> InputStream, length: Long) {
        val base = serverUrl.value
        if (base.isBlank()) {
            _uploadState.value = UploadState.Error("未配置服务器地址")
            return
        }
        val parent = _path.value.trimEnd('/')
        val dest = if (parent.isEmpty()) "/$name" else "$parent/$name"
        _uploadState.value = UploadState.Loading
        viewModelScope.launch {
            _uploadState.value = try {
                val ok = repository.uploadFile(base, dest, content, length)
                if (ok) {
                    load()
                    UploadState.Success("上传完成")
                } else {
                    UploadState.Error("上传失败")
                }
            } catch (e: Exception) {
                UploadState.Error(e.message ?: "上传失败")
            }
        }
    }

    fun clearUploadState() {
        _uploadState.value = UploadState.Idle
    }
}

sealed interface FilesState {
    data object Idle : FilesState
    data object Loading : FilesState
    data class Success(val items: List<FileItem>, val total: Int) : FilesState
    data class Error(val message: String) : FilesState
}

sealed interface UploadState {
    data object Idle : UploadState
    data object Loading : UploadState
    data class Success(val message: String) : UploadState
    data class Error(val message: String) : UploadState
}
