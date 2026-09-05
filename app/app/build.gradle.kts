plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

// Kotlin 编译用 JDK 21（jvmToolchain）：
// Kotlin 1.9.24 嵌入的 JavaVersion.parse 无法解析 JDK 25 的三段版本号
// （如 25.0.2），会在 CI/开发者本机 JDK 高于 21 时直接抛 IllegalArgumentException。
// toolchain 让 Gradle 从本地已安装 JDK 中自动选 21 用于 kotlinc/javac，
// 与 gradle daemon 本身跑的 JDK 解耦，避免版本解析崩溃。
kotlin {
    jvmToolchain(21)
}

android {
    namespace = "io.github.pelico.ddnas"
    compileSdk = 34

    defaultConfig {
        applicationId = "io.github.pelico.ddnas"
        minSdk = 24
        targetSdk = 34
        versionCode = 2
        versionName = "1.1"

        vectorDrawables {
            useSupportLibrary = true
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            proguardFiles(
                getDefaultProguardFile("proguard-android-optimize.txt"),
                "proguard-rules.pro"
            )
        }
    }

    // Kotlin/AGP 使用的 JDK 统一锁定 21：
    // Kotlin 1.9 的 JavaVersion.parse 在 JDK 25（如 25.0.2 三段）上会抛
    // IllegalArgumentException，导致 CI 环境 JDK 版本异常时直接编译失败。
    // 锁定 toolchain 后自动从本机已安装 JDK 中选 21，与运行 gradle 的 JDK 解耦。
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    kotlinOptions {
        jvmTarget = "17"
    }

    buildFeatures {
        compose = true
    }

    composeOptions {
        kotlinCompilerExtensionVersion = "1.5.14"
    }

    packaging {
        resources {
            excludes += "/META-INF/{AL2.0,LGPL2.1}"
        }
    }
}

dependencies {
    val composeBom = platform("androidx.compose:compose-bom:2024.06.00")
    implementation(composeBom)

    implementation("androidx.core:core-ktx:1.13.1")
    implementation("androidx.lifecycle:lifecycle-runtime-ktx:2.8.3")
    implementation("androidx.lifecycle:lifecycle-runtime-compose:2.8.3")
    implementation("androidx.activity:activity-compose:1.9.0")

    implementation("androidx.compose.ui:ui")
    implementation("androidx.compose.ui:ui-graphics")
    implementation("androidx.compose.ui:ui-tooling-preview")
    implementation("androidx.compose.material3:material3")
    implementation("androidx.compose.material:material-icons-extended")

    implementation("androidx.datastore:datastore-preferences:1.1.1")
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-android:1.8.1")

    // SAF 文档树遍历（备份选目录后递归上传）。
    implementation("androidx.documentfile:documentfile:1.0.1")

    // WorkManager：定时增量备份调度（PeriodicWorkRequest + 充电/Wi-Fi 约束）。
    implementation("androidx.work:work-runtime-ktx:2.9.0")

    // 网络层：仅 OkHttp（WebView 同源 cookie 会话；ExoPlayer 与备份服务注入 cookie 头）。
    implementation("com.squareup.okhttp3:okhttp:4.12.0")

    // 媒体播放：ExoPlayer + OkHttp 数据源（携带 cookie 鉴权 Range 请求）。
    implementation("androidx.media3:media3-exoplayer:1.3.1")
    implementation("androidx.media3:media3-ui:1.3.1")
    implementation("androidx.media3:media3-datasource-okhttp:1.3.1")
    // MediaSessionCompat + NotificationCompat.MediaStyle（音乐前台通知/锁屏控制）
    implementation("androidx.media:media:1.7.0")

    debugImplementation("androidx.compose.ui:ui-tooling")
}
