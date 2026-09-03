package io.github.pelico.ddnas.ui.files

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import io.github.pelico.ddnas.DdnasApplication
import io.github.pelico.ddnas.data.model.FileItem
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import java.io.InputStream

class FilesViewModel(app: Application) : AndroidViewModel(app) {

    private val settings = getApplication<DdnasApplication>().settings
    private val repository = getApplication<DdnasApplication>().repository

    private val _path = MutableStateFlow("/")
    val path: StateFlow<String> = _path.asStateFlow()

    private val _state = MutableStateFlow<FilesState>(FilesState.Idle)
    val state: StateFlow<FilesState> = _state.asStateFlow()

    private val _uploadState = MutableStateFlow<UploadState>(UploadState.Idle)
    val uploadState: StateFlow<UploadState> = _uploadState.asStateFlow()

    // 最近一次 load/upload 读到的 base 地址缓存，供同步的 streamUrlFor/baseUrl 使用。
    // 这两个方法只在列表加载成功后（Success 态）被调用，故缓存必已就绪。
    @Volatile
    private var currentBase: String = ""

    fun load() {
        _state.value = FilesState.Loading
        viewModelScope.launch {
            // 直接从 DataStore 读持久化地址，避免依赖未被 UI 订阅的
            // WhileSubscribed StateFlow（.value 会停在初始空串，误报"未配置地址"）
            currentBase = settings.serverUrl.first()
            _state.value = if (currentBase.isBlank()) {
                FilesState.Error("未配置服务器地址")
            } else {
                try {
                    val resp = repository.listFiles(currentBase, _path.value)
                    FilesState.Success(resp.items, resp.total)
                } catch (e: Exception) {
                    FilesState.Error(e.message ?: "加载文件列表失败")
                }
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
        return repository.streamUrl(currentBase, "$dir/$name")
    }

    fun upload(name: String, content: () -> InputStream, length: Long) {
        _uploadState.value = UploadState.Loading
        viewModelScope.launch {
            currentBase = settings.serverUrl.first()
            if (currentBase.isBlank()) {
                _uploadState.value = UploadState.Error("未配置服务器地址")
                return@launch
            }
            val parent = _path.value.trimEnd('/')
            val dest = if (parent.isEmpty()) "/$name" else "$parent/$name"
            _uploadState.value = try {
                val ok = repository.uploadFile(currentBase, dest, content, length)
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
