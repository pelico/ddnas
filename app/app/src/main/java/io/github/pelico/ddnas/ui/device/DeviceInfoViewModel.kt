package io.github.pelico.ddnas.ui.device

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import io.github.pelico.ddnas.DdnasApplication
import io.github.pelico.ddnas.data.model.SystemInfo
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch

class DeviceInfoViewModel(app: Application) : AndroidViewModel(app) {

    private val settings = getApplication<DdnasApplication>().settings
    private val repository = getApplication<DdnasApplication>().repository

    private val serverUrl: StateFlow<String> = settings.serverUrl
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5000), "")

    private val _state = MutableStateFlow<DeviceInfoState>(DeviceInfoState.Idle)
    val state: StateFlow<DeviceInfoState> = _state.asStateFlow()

    fun load() {
        val base = serverUrl.value
        if (base.isBlank()) {
            _state.value = DeviceInfoState.Error("未配置服务器地址，请先到设置页填写")
            return
        }
        _state.value = DeviceInfoState.Loading
        viewModelScope.launch {
            _state.value = try {
                DeviceInfoState.Success(repository.systemInfo(base))
            } catch (e: Exception) {
                DeviceInfoState.Error(e.message ?: "加载设备信息失败")
            }
        }
    }
}

sealed interface DeviceInfoState {
    data object Idle : DeviceInfoState
    data object Loading : DeviceInfoState
    data class Success(val info: SystemInfo) : DeviceInfoState
    data class Error(val message: String) : DeviceInfoState
}
