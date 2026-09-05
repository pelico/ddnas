package io.github.pelico.ddnas.data

import android.content.Context
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.intPreferencesKey
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.map

private val Context.serversDataStore by preferencesDataStore(name = "ddnas_servers")

/**
 * 一个已配置的 DDNAS 中间件服务器。
 * url 为形如 http://192.168.1.10:8080 的根地址（不含路径）。
 */
data class Server(val name: String, val url: String)

/**
 * 服务器列表持久化：用 DataStore 存两键——
 *  - servers：每行 "名称\u0001地址"，\u0001 作分隔（URL 不会含此字符）；
 *  - active：当前选中的服务器下标。
 * 无需 kotlinx-serialization，解析/序列化在此内联实现。
 */
class ServerStore(private val context: Context) {

    private val serversKey = stringPreferencesKey("servers")
    private val activeKey = intPreferencesKey("active")

    val servers: Flow<List<Server>> = context.serversDataStore.data.map { it[serversKey]?.parseServers() ?: emptyList() }
    val activeIndex: Flow<Int> = context.serversDataStore.data.map { it[activeKey] ?: 0 }

    suspend fun add(name: String, url: String) {
        context.serversDataStore.edit { it[serversKey] = (it[serversKey]?.parseServers() ?: emptyList()).plus(Server(name.trim(), normalizeUrl(url))).joinString() }
    }

    suspend fun update(index: Int, name: String, url: String) {
        context.serversDataStore.edit { prefs ->
            val list = (prefs[serversKey]?.parseServers() ?: emptyList()).toMutableList()
            if (index in list.indices) list[index] = Server(name.trim(), normalizeUrl(url))
            prefs[serversKey] = list.joinString()
        }
    }

    suspend fun delete(index: Int) {
        context.serversDataStore.edit { prefs ->
            val list = (prefs[serversKey]?.parseServers() ?: emptyList()).toMutableList()
            if (index in list.indices) list.removeAt(index)
            prefs[serversKey] = list.joinString()
            // 删除后修正 active 指向
            val cur = prefs[activeKey] ?: 0
            prefs[activeKey] = when {
                list.isEmpty() -> 0
                index < cur -> cur - 1
                index == cur -> 0
                else -> cur
            }
        }
    }

    suspend fun setActive(index: Int) {
        context.serversDataStore.edit { it[activeKey] = index }
    }

    private fun List<Server>.joinString(): String =
        joinToString("\n") { "${it.name}\u0001${it.url}" }

    private fun String.parseServers(): List<Server> =
        split("\n").mapNotNull { line ->
            val p = line.split("\u0001")
            if (p.size == 2 && p[1].isNotBlank()) Server(p[0], normalizeUrl(p[1])) else null
        }

    /** 规范化服务器地址：缺省协议时自动补全，避免用户漏写 http(s) 导致请求失败。
     *  - 已有 http:// 或 https:// 前缀（不区分大小写）：仅去尾部 /
     *  - Cloudflare 相关域名（trycloudflare.com / workers.dev 等）补 https://
     *  - 其余（局域网 IP/域名）默认补 http:// */
    private fun normalizeUrl(raw: String): String {
        val u = raw.trim().trimEnd('/')
        val lower = u.lowercase()
        if (lower.startsWith("http://") || lower.startsWith("https://")) return u
        val scheme = if (lower.contains("trycloudflare") || lower.contains("workers.dev") || lower.contains("cloudflare")) {
            "https://"
        } else {
            "http://"
        }
        return scheme + u
    }
}
