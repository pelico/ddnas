package io.github.pelico.ddnas.ui.settings

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import io.github.pelico.ddnas.DdnasApplication
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch

/**
 * 设置页 ViewModel。
 *
 * 用本地草稿态绑定输入框，避免"每敲一个字就写 DataStore"的隐式行为；
 * 由显式 [save] 按钮提交持久化，并提供"已保存"反馈。
 * 草稿初始值从 DataStore 读出（suspend first()），不依赖 StateFlow 订阅。
 */
class SettingsViewModel(app: Application) : AndroidViewModel(app) {

    private val settings = getApplication<DdnasApplication>().settings
    private val repository = getApplication<DdnasApplication>().repository

    private val _url = MutableStateFlow("")
    private val _token = MutableStateFlow("")
    val url: StateFlow<String> = _url.asStateFlow()
    val token: StateFlow<String> = _token.asStateFlow()

    private val _saved = MutableStateFlow(true)
    val saved: StateFlow<Boolean> = _saved.asStateFlow()

    private val _testState = MutableStateFlow<TestState>(TestState.Idle)
    val testState: StateFlow<TestState> = _testState.asStateFlow()

    init {
        viewModelScope.launch {
            _url.value = settings.serverUrl.first()
            _token.value = settings.appToken.first()
        }
    }

    fun setUrl(value: String) {
        _url.value = value
        _saved.value = false
    }

    fun setToken(value: String) {
        _token.value = value
        _saved.value = false
    }

    fun save() {
        viewModelScope.launch {
            settings.setServerUrl(_url.value.trim())
            settings.setAppToken(_token.value.trim())
            _saved.value = true
        }
    }

    fun testConnection() {
        val base = _url.value.trim()
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
