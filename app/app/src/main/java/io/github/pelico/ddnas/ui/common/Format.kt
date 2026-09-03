package io.github.pelico.ddnas.ui.common

import java.util.Locale
import kotlin.math.ln
import kotlin.math.pow

/** Formats a byte count into a human-readable string (KB/MB/GB/...). */
fun formatBytes(bytes: Double): String {
    if (bytes.isNaN() || bytes <= 0.0) return "0 B"
    val units = arrayOf("B", "KB", "MB", "GB", "TB", "PB")
    val digitGroups = (ln(bytes) / ln(1024.0)).toInt().coerceIn(0, units.lastIndex)
    val value = bytes / 1024.0.pow(digitGroups.toDouble())
    return String.format(Locale.US, "%.1f %s", value, units[digitGroups])
}

fun formatBytes(bytes: Long): String = formatBytes(bytes.toDouble())

fun formatBytes(bytes: Int): String = formatBytes(bytes.toLong())

/** Formats an uptime in seconds into "x天x小时" / "x小时x分钟" / "x分钟". */
fun formatUptime(seconds: Double): String {
    val totalSec = seconds.toLong().coerceAtLeast(0L)
    val days = totalSec / 86400
    val hours = (totalSec % 86400) / 3600
    val minutes = (totalSec % 3600) / 60
    return buildString {
        if (days > 0) append("${days}天")
        if (hours > 0 || days > 0) append("${hours}小时")
        if (days == 0L && (minutes > 0 || (days == 0L && hours == 0L))) append("${minutes}分钟")
    }.ifEmpty { "0分钟" }
}

/** Formats a 0..100-ish percentage value with one decimal. */
fun formatPercent(value: Double): String =
    String.format(Locale.US, "%.1f%%", value)

/** Formats a load average (e.g. 0.42 -> "0.42"). */
fun formatLoad(value: Double): String =
    String.format(Locale.US, "%.2f", value)

/** True if [name] looks like a media file the player can stream. */
fun isMediaFile(name: String): Boolean {
    val lower = name.substringAfterLast('.', "").lowercase(Locale.US)
    return lower in setOf(
        "mp4", "mkv", "webm", "mov", "avi", "flv", "ts", "m4v", "3gp",
        "mp3", "aac", "flac", "ogg", "wav", "m4a", "opus"
    )
}
