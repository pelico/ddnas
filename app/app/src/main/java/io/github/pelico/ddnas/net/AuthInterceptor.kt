package io.github.pelico.ddnas.net

import okhttp3.Interceptor
import okhttp3.Response

/**
 * OkHttp interceptor that injects `Authorization: Bearer <token>` when a token
 * is present. The token is held in a volatile field that [DdnasApplication]
 * keeps in sync with the DataStore-backed [io.github.pelico.ddnas.data.Settings].
 */
class AuthInterceptor : Interceptor {

    @Volatile
    var token: String? = null

    override fun intercept(chain: Interceptor.Chain): Response {
        val request = chain.request()
        val token = token
        return if (!token.isNullOrEmpty()) {
            chain.proceed(
                request.newBuilder()
                    .header("Authorization", "Bearer $token")
                    .build()
            )
        } else {
            chain.proceed(request)
        }
    }
}
