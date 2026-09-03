package io.github.pelico.ddnas.ui.nav

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Computer
import androidx.compose.material.icons.filled.Folder
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.navigation.NavGraph.Companion.findStartDestination
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.currentBackStackEntryAsState
import androidx.navigation.compose.rememberNavController
import io.github.pelico.ddnas.DdnasApplication
import io.github.pelico.ddnas.ui.device.DeviceInfoScreen
import io.github.pelico.ddnas.ui.files.FilesScreen
import io.github.pelico.ddnas.ui.player.PlayerLauncher
import io.github.pelico.ddnas.ui.player.PlayerScreen
import io.github.pelico.ddnas.ui.settings.SettingsScreen
import kotlinx.coroutines.flow.first

private object Routes {
    const val DEVICE = "device"
    const val FILES = "files"
    const val SETTINGS = "settings"
    const val PLAYER = "player"
}

private data class Tab(
    val route: String,
    val label: String,
    val icon: ImageVector
)

private val tabs = listOf(
    Tab(Routes.DEVICE, "设备", Icons.Filled.Computer),
    Tab(Routes.FILES, "文件", Icons.Filled.Folder),
    Tab(Routes.SETTINGS, "设置", Icons.Filled.Settings)
)

@Composable
fun DdnasNavGraph() {
    val app = LocalContext.current.applicationContext as DdnasApplication
    val navController = rememberNavController()

    // Determine the start destination once: land on Settings until a server
    // address has been configured.
    var startDestination by remember { mutableStateOf<String?>(null) }
    LaunchedEffect(Unit) {
        startDestination = if (app.settings.serverUrl.first().isBlank()) {
            Routes.SETTINGS
        } else {
            Routes.DEVICE
        }
    }

    val current = navController.currentBackStackEntryAsState().value?.destination?.route
    val showBottomBar = current in tabs.map { it.route }

    val resolvedStart = startDestination
    if (resolvedStart == null) {
        Box(modifier = Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
            CircularProgressIndicator()
        }
        return
    }

    Scaffold(
        bottomBar = {
            if (showBottomBar) {
                NavigationBar {
                    tabs.forEach { tab ->
                        NavigationBarItem(
                            selected = current == tab.route,
                            onClick = {
                                navController.navigate(tab.route) {
                                    popUpTo(navController.graph.findStartDestination().id) {
                                        saveState = true
                                    }
                                    launchSingleTop = true
                                    restoreState = true
                                }
                            },
                            icon = { Icon(tab.icon, contentDescription = tab.label) },
                            label = { Text(tab.label) }
                        )
                    }
                }
            }
        }
    ) { padding ->
        NavHost(
            navController = navController,
            startDestination = resolvedStart,
            modifier = Modifier.fillMaxSize()
        ) {
            composable(Routes.DEVICE) {
                DeviceInfoScreen(contentPadding = padding)
            }
            composable(Routes.FILES) {
                FilesScreen(
                    contentPadding = padding,
                    onPlay = { navController.navigate(Routes.PLAYER) }
                )
            }
            composable(Routes.SETTINGS) {
                SettingsScreen(contentPadding = padding)
            }
            composable(Routes.PLAYER) {
                PlayerScreen(
                    streamUrl = PlayerLauncher.pendingStreamUrl,
                    onBack = { navController.popBackStack() }
                )
            }
        }
    }
}
