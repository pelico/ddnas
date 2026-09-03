package io.github.pelico.ddnas

import android.app.Application
import io.github.pelico.ddnas.data.DdnasRepository
import io.github.pelico.ddnas.data.Settings
import io.github.pelico.ddnas.net.RetrofitProvider
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.launchIn
import kotlinx.coroutines.flow.onEach

/**
 * Application entry point. Owns the single [Settings], [DdnasRepository] and
 * keeps the [RetrofitProvider.authInterceptor] token in sync with persisted storage.
 */
class DdnasApplication : Application() {

    val settings: Settings by lazy { Settings(this) }
    val repository: DdnasRepository by lazy { DdnasRepository() }

    private val appScope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)

    override fun onCreate() {
        super.onCreate()
        // Keep the network auth header up-to-date with whatever the user saved.
        settings.appToken
            .onEach { token -> RetrofitProvider.authInterceptor.token = token.ifBlank { null } }
            .launchIn(appScope)
    }
}
