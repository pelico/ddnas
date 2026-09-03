package io.github.pelico.ddnas.data

import android.content.Context
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.map

private val Context.dataStore by preferencesDataStore(name = "ddnas_settings")

/**
 * Persisted user configuration backed by Preferences DataStore.
 * Holds the middleware base URL and the app Bearer token.
 */
class Settings(private val context: Context) {

    val serverUrl: Flow<String> = context.dataStore.data.map { it[SERVER_URL] ?: "" }
    val appToken: Flow<String> = context.dataStore.data.map { it[APP_TOKEN] ?: "" }

    suspend fun setServerUrl(value: String) {
        context.dataStore.edit { it[SERVER_URL] = value.trim() }
    }

    suspend fun setAppToken(value: String) {
        context.dataStore.edit { it[APP_TOKEN] = value.trim() }
    }

    private companion object {
        private val SERVER_URL = stringPreferencesKey("server_url")
        private val APP_TOKEN = stringPreferencesKey("app_token")
    }
}
