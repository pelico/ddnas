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
}
