package io.github.pelico.ddnas.ui.device

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import io.github.pelico.ddnas.data.model.SystemInfo
import io.github.pelico.ddnas.ui.common.formatBytes
import io.github.pelico.ddnas.ui.common.formatLoad
import io.github.pelico.ddnas.ui.common.formatPercent
import io.github.pelico.ddnas.ui.common.formatUptime

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun DeviceInfoScreen(
    contentPadding: PaddingValues,
    viewModel: DeviceInfoViewModel = viewModel()
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    LaunchedEffect(Unit) {
        if (state is DeviceInfoState.Idle) viewModel.load()
    }

    Scaffold(
        modifier = Modifier.fillMaxSize(),
        topBar = {
            TopAppBar(
                title = { Text("设备信息") },
                actions = {
                    IconButton(onClick = { viewModel.load() }) {
                        Icon(Icons.Filled.Refresh, contentDescription = "刷新")
                    }
                }
            )
        }
    ) { innerPadding ->
        when (val current = state) {
            is DeviceInfoState.Loading, DeviceInfoState.Idle -> CenterLoading(
                Modifier.padding(innerPadding).padding(contentPadding)
            )
            is DeviceInfoState.Error -> CenterError(
                message = current.message,
                onRetry = { viewModel.load() },
                modifier = Modifier.padding(innerPadding).padding(contentPadding)
            )
            is DeviceInfoState.Success -> DeviceInfoContent(
                info = current.info,
                modifier = Modifier
                    .padding(innerPadding)
                    .padding(contentPadding)
            )
        }
    }
}

@Composable
private fun DeviceInfoContent(info: SystemInfo, modifier: Modifier) {
    Column(
        modifier = modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp)
    ) {
        InfoCard(title = "主机") {
            InfoRow("主机名", info.hostname)
            InfoRow("操作系统", info.os)
            InfoRow("内核", info.kernel)
            InfoRow("架构", info.arch)
            InfoRow("运行时长", formatUptime(info.uptime_seconds))
        }

        InfoCard(title = "CPU（${info.cpu.cores} 核）") {
            UsageBar(label = "使用率", percent = info.cpu.usage_percent)
            InfoRow("负载 1/5/15", "${formatLoad(info.cpu.load1)}  ${formatLoad(info.cpu.load5)}  ${formatLoad(info.cpu.load15)}")
        }

        InfoCard(title = "内存") {
            UsageBar(label = "使用率", percent = info.memory.usage_percent)
            InfoRow("总量", formatBytes(info.memory.total_bytes))
            InfoRow("可用", formatBytes(info.memory.available_bytes))
            InfoRow("已用", formatBytes(info.memory.used_bytes))
        }

        info.disks.forEach { disk ->
            InfoCard(title = "磁盘 ${disk.mountpoint}") {
                InfoRow("设备", disk.device)
                InfoRow("文件系统", disk.fstype)
                InfoRow("总量", formatBytes(disk.total_bytes))
                InfoRow("已用", formatBytes(disk.used_bytes))
                UsageBar(label = "使用率", percent = disk.usage_percent)
            }
        }

        info.network.forEach { iface ->
            InfoCard(title = "网络 ${iface.device}") {
                InfoRow("接收", formatBytes(iface.rx_bytes))
                InfoRow("发送", formatBytes(iface.tx_bytes))
            }
        }
    }
}

@Composable
private fun InfoCard(title: String, content: @Composable () -> Unit) {
    Card(
        modifier = Modifier.fillMaxWidth(),
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceVariant)
    ) {
        Column(modifier = Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Text(title, style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
            content()
        }
    }
}

@Composable
private fun InfoRow(label: String, value: String) {
    Row(
        modifier = Modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.SpaceBetween
    ) {
        Text(label, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
        Text(value, style = MaterialTheme.typography.bodyMedium)
    }
}

@Composable
private fun UsageBar(label: String, percent: Double) {
    Column(modifier = Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(4.dp)) {
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.SpaceBetween
        ) {
            Text(label, style = MaterialTheme.typography.bodySmall)
            Text(formatPercent(percent), style = MaterialTheme.typography.bodySmall)
        }
        LinearProgressIndicator(
            progress = { (percent / 100f).toFloat().coerceIn(0f, 1f) },
            modifier = Modifier.fillMaxWidth()
        )
    }
}

@Composable
private fun CenterLoading(modifier: Modifier) {
    androidx.compose.foundation.layout.Box(
        modifier = modifier.fillMaxSize(),
        contentAlignment = Alignment.Center
    ) { CircularProgressIndicator() }
}

@Composable
private fun CenterError(message: String, onRetry: () -> Unit, modifier: Modifier) {
    androidx.compose.foundation.layout.Box(
        modifier = modifier.fillMaxSize(),
        contentAlignment = Alignment.Center
    ) {
        Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Text(message, color = MaterialTheme.colorScheme.error)
            androidx.compose.material3.TextButton(onClick = onRetry) { Text("重试") }
        }
    }
}
