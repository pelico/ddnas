package io.github.pelico.ddnas.data.model

import kotlinx.serialization.Serializable

/**
 * Models for the DDNAS middleware JSON contract.
 *
 * Property names intentionally mirror the wire JSON keys (snake_case) so that
 * kotlinx.serialization maps fields 1:1 without any [@SerialName] annotations.
 */

@Serializable
data class HealthResponse(
    val ok: Boolean = false
)

@Serializable
data class AdaptersResponse(
    val adapters: List<Adapter> = emptyList()
)

@Serializable
data class Adapter(
    val name: String = "",
    val enabled: Boolean = false,
    val capabilities: List<String> = emptyList(),
    val routes: List<Route> = emptyList()
)

@Serializable
data class Route(
    val method: String = "",
    val path: String = "",
    val desc: String = ""
)

@Serializable
data class SystemInfo(
    val hostname: String = "",
    val os: String = "",
    val kernel: String = "",
    val arch: String = "",
    val uptime_seconds: Double = 0.0,
    val cpu: Cpu = Cpu(),
    val memory: Memory = Memory(),
    val disks: List<Disk> = emptyList(),
    val network: List<NetworkInterface> = emptyList()
)

@Serializable
data class Cpu(
    val cores: Int = 0,
    val load1: Double = 0.0,
    val load5: Double = 0.0,
    val load15: Double = 0.0,
    val usage_percent: Double = 0.0
)

@Serializable
data class Memory(
    val total_bytes: Double = 0.0,
    val available_bytes: Double = 0.0,
    val used_bytes: Double = 0.0,
    val usage_percent: Double = 0.0
)

@Serializable
data class Disk(
    val device: String = "",
    val mountpoint: String = "",
    val fstype: String = "",
    val total_bytes: Double = 0.0,
    val used_bytes: Double = 0.0,
    val usage_percent: Double = 0.0
)

@Serializable
data class NetworkInterface(
    val device: String = "",
    val rx_bytes: Double = 0.0,
    val tx_bytes: Double = 0.0
)

@Serializable
data class FileListResponse(
    val items: List<FileItem> = emptyList(),
    val total: Int = 0
)

@Serializable
data class FileItem(
    val name: String = "",
    val is_dir: Boolean = false,
    val size: Long = 0L,
    val modified: String = "",
    val sign: String = ""
)
