package io.github.pelico.ddnas.ui.theme

import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.runtime.Composable

private val DDNASDarkColorScheme = darkColorScheme(
    primary = Indigo80,
    onPrimary = SurfaceDark,
    primaryContainer = SurfaceVariantDark,
    onPrimaryContainer = Indigo80,
    secondary = Cyan80,
    onSecondary = SurfaceDark,
    tertiary = Cyan80,
    background = SurfaceDark,
    onBackground = OnSurfaceDark,
    surface = SurfaceDark,
    onSurface = OnSurfaceDark,
    surfaceVariant = SurfaceVariantDark,
    onSurfaceVariant = OnSurfaceVariantDark,
    outline = IndigoGrey60
)

/**
 * App-wide theme. Forced dark as per the design brief (NAS dashboard reads best
 * on dark surfaces).
 */
@Composable
fun DDNASTheme(content: @Composable () -> Unit) {
    MaterialTheme(
        colorScheme = DDNASDarkColorScheme,
        typography = Typography,
        content = content
    )
}
