package io.github.pelico.ddnas.ui.settings

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import io.github.pelico.ddnas.DdnasApplication
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch

class SettingsViewModel(app: Application) : AndroidViewModel(app) {

    private val settings = getApplication<DdnasApplication>().settings
    private val repository = getApplication<DdnasApplication>().repository

    val serverUrl: StateFlow<String> = settings.serverUrl
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5000), "")

    val appToken: StateFlow<String> = settings.appToken
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5000), "")

    private val _testState = MutableStateFlow<TestState>(TestState.Idle)
    val testState: StateFlow<TestState> = _testState.asStateFlow()

    fun setServerUrl(value: String) {
        viewModelScope.launch { settings.setServerUrl(value) }
    }

    fun setAppToken(value: String) {
        viewModelScope.launch { settings.setAppToken(value) }
    }

    fun testConnection() {
        val base = serverUrl.value
        if (base.isBlank()) {
            _testState.value = TestState.Error("请先填写服务器地址")
            return
        }
        _testState.value = TestState.Loading
        viewModelScope.launch {
            _testState.value = try {
                val result = repository.health(base)
                if (result.ok) TestState.Success("连接成功") else TestState.Error("服务器未就绪")
            } catch (e: Exception) {
                TestState.Error(e.message ?: "连接失败")
            }
        }
    }
}

sealed interface TestState {
    data object Idle : TestState
    data object Loading : TestState
    data class Success(val message: String) : TestState
    data class Error(val message: String) : TestState
}
