package io.github.pelico.ddnas.data

import io.github.pelico.ddnas.data.model.AdaptersResponse
import io.github.pelico.ddnas.data.model.FileListResponse
import io.github.pelico.ddnas.data.model.HealthResponse
import io.github.pelico.ddnas.data.model.SystemInfo
import retrofit2.http.GET
import retrofit2.http.Query
import retrofit2.http.Url

/**
 * Retrofit API surface for the DDNAS Go middleware.
 *
 * Every method takes an absolute URL via [@Url] so the base address can be
 * supplied at call time (read from user settings) without rebuilding the
 * Retrofit instance. The Bearer token header is injected by [AuthInterceptor].
 */
interface DdnasApi {

    @GET
    suspend fun health(@Url url: String): HealthResponse

    @GET
    suspend fun adapters(@Url url: String): AdaptersResponse

    @GET
    suspend fun systemInfo(@Url url: String): SystemInfo

    @GET
    suspend fun listFiles(
        @Url url: String,
        @Query("path") path: String
    ): FileListResponse
}
