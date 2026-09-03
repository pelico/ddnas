package io.github.pelico.ddnas.net

import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.logging.HttpLoggingInterceptor
import kotlinx.serialization.json.Json
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory
import io.github.pelico.ddnas.data.DdnasApi
import java.util.concurrent.TimeUnit

/**
 * Single shared network stack.
 *
 * - The Retrofit baseUrl is a placeholder; every API call supplies a full
 *   absolute URL via [@Url], so the base address comes from user settings.
 * - [okHttpClient] is also reused for direct streaming uploads and as the
 *   ExoPlayer OkHttp data source so the Bearer header applies everywhere.
 */
object RetrofitProvider {

    val json: Json = Json {
        ignoreUnknownKeys = true
        coerceInputValues = true
        explicitNulls = false
        isLenient = true
    }

    val authInterceptor: AuthInterceptor = AuthInterceptor()

    private val loggingInterceptor = HttpLoggingInterceptor().apply {
        level = HttpLoggingInterceptor.Level.BASIC
    }

    val okHttpClient: OkHttpClient = OkHttpClient.Builder()
        .addInterceptor(authInterceptor)
        .addInterceptor(loggingInterceptor)
        .connectTimeout(30, TimeUnit.SECONDS)
        .readTimeout(60, TimeUnit.SECONDS)
        .writeTimeout(120, TimeUnit.SECONDS)
        .retryOnConnectionFailure(true)
        .build()

    private val contentType = "application/json".toMediaType()

    val retrofit: Retrofit = Retrofit.Builder()
        .baseUrl("http://localhost:8080/")
        .client(okHttpClient)
        .addConverterFactory(json.asConverterFactory(contentType))
        .build()

    val api: DdnasApi = retrofit.create(DdnasApi::class.java)
}
