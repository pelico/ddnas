package io.github.pelico.ddnas.data

import android.content.Context
import androidx.datastore.preferences.core.booleanPreferencesKey
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.map

private val Context.backupDataStore by preferencesDataStore(name = "ddnas_backup")

/** 备份配置，持久化到 DataStore。 */
data class BackupConfig(
    val treeUri: String = "",
    val remoteBase: String = "/手机备份/",
    val autoBackup: Boolean = false,
    val lastBackupTime: Long = 0,
)

/** 备份配置持久化。SAF tree URI 持久化后无需每次重选目录。 */
class BackupStore(private val context: Context) {
    private val treeUriKey = stringPreferencesKey("tree_uri")
    private val remoteBaseKey = stringPreferencesKey("remote_base")
    private val autoBackupKey = booleanPreferencesKey("auto_backup")
    private val lastTimeKey = stringPreferencesKey("last_time")

    val config: Flow<BackupConfig> = context.backupDataStore.data.map {
        BackupConfig(
            treeUri = it[treeUriKey] ?: "",
            remoteBase = it[remoteBaseKey] ?: "/手机备份/",
            autoBackup = it[autoBackupKey] ?: false,
            lastBackupTime = (it[lastTimeKey] ?: "0").toLongOrNull() ?: 0,
        )
    }

    suspend fun get(): BackupConfig = config.first()

    suspend fun setTreeUri(uri: String) = context.backupDataStore.edit { it[treeUriKey] = uri }.let {}
    suspend fun setRemoteBase(base: String) = context.backupDataStore.edit { it[remoteBaseKey] = base }.let {}
    suspend fun setAutoBackup(on: Boolean) = context.backupDataStore.edit { it[autoBackupKey] = on }.let {}
    suspend fun setLastBackupTime(ts: Long) = context.backupDataStore.edit { it[lastTimeKey] = ts.toString() }.let {}
}

/**
 * 备份清单：记录已上传文件的 size + lastModified，用于增量备份。
 * 用 SharedPreferences（key=treeUriHash:remoteBaseHash:relPath, value="size|mtime"），下次只传变更的文件。
 * key 同时加 treeUri 与 remoteBase 前缀隔离：
 * - 切换本地源目录 → treeUri 变，独立命名空间
 * - 切换远程目标位置 → remoteBase 变，独立命名空间，避免"在 A 备份后切到 B 误判已备份"
 *
 * 远端同步：备份完成后把 manifest 序列化成 JSON 上传到 <remoteBase>/.ddnas_manifest.json，
 * 下次备份前先下载并合并。这样 app 重装/换设备后不丢失增量历史，不必全量重传。
 * 多设备共享同一远端目录时不需要显式隔离——size+mtime 天然区分不同文件。
 */
class BackupManifest(context: Context, treeUri: String, remoteBase: String) {
    private val prefs = context.getSharedPreferences("ddnas_backup_manifest", 0)
    // 用 treeUri + remoteBase 的 hashCode 做命名空间前缀，避免不同目录树/不同目标位置的同名文件误判
    private val prefix = treeUri.hashCode().toString(16) + ":" + remoteBase.hashCode().toString(16) + ":"

    /** 返回文件是否需要上传（size 或 mtime 变了）。 */
    fun needUpload(relPath: String, size: Long, mtime: Long): Boolean {
        val prev = prefs.getString(prefix + relPath, null) ?: return true
        val parts = prev.split("|")
        if (parts.size != 2) return true
        return parts[0].toLongOrNull() != size || parts[1].toLongOrNull() != mtime
    }

    fun markUploaded(relPath: String, size: Long, mtime: Long) {
        prefs.edit().putString(prefix + relPath, "$size|$mtime").apply()
    }

    fun clear() = prefs.edit().clear().apply()

    /** 序列化当前命名空间的条目为 JSON，用于上传到远端做 manifest 同步。
     * 格式: {"version":1,"entries":{"relPath":"size|mtime",...}} */
    fun toJson(): String {
        val entries = org.json.JSONObject()
        val all = prefs.all
        for ((k, v) in all) {
            if (k.startsWith(prefix) && v is String) {
                val rel = k.substring(prefix.length)
                entries.put(rel, v)
            }
        }
        val json = org.json.JSONObject()
        json.put("version", 1)
        json.put("entries", entries)
        return json.toString()
    }

    /** 从远端下载的 JSON 合并到本地 manifest。
     * 只添加本地不存在的条目（本地条目优先，反映本设备实际上传状态）。
     * 这样换设备后能继承前设备的增量历史，相同文件不重传。 */
    fun mergeFromJson(json: String) {
        try {
            val obj = org.json.JSONObject(json)
            val entries = obj.optJSONObject("entries") ?: return
            val editor = prefs.edit()
            val keys = entries.keys()
            while (keys.hasNext()) {
                val rel = keys.next()
                val valStr = entries.optString(rel, "")
                if (valStr.isEmpty()) continue
                val localKey = prefix + rel
                if (prefs.getString(localKey, null) == null) {
                    editor.putString(localKey, valStr)
                }
            }
            editor.apply()
        } catch (_: Exception) { }
    }
}
