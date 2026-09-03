package io.github.pelico.ddnas.ui.device

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import io.github.pelico.ddnas.DdnasApplication
import io.github.pelico.ddnas.data.model.SystemInfo
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch

class DeviceInfoViewModel(app: Application) : AndroidViewModel(app) {

    private val settings = getApplication<DdnasApplication>().settings
    private val repository = getApplication<DdnasApplication>().repository

    private val _state = MutableStateFlow<DeviceInfoState>(DeviceInfoState.Idle)
    val state: StateFlow<DeviceInfoState> = _state.asStateFlow()

    fun load() {
        _state.value = DeviceInfoState.Loading
        viewModelScope.launch {
            // 直接从 DataStore 读当前持久化的地址，避免依赖未被 UI 订阅的
            // WhileSubscribed StateFlow（其 .value 会停在初始空串，导致误报"未配置地址"）
            val base = settings.serverUrl.first()
            _state.value = if (base.isBlank()) {
                DeviceInfoState.Error("未配置服务器地址，请先到设置页填写")
            } else {
                try {
                    DeviceInfoState.Success(repository.systemInfo(base))
                } catch (e: Exception) {
                    DeviceInfoState.Error(e.message ?: "加载设备信息失败")
                }
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
