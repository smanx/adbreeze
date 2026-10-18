plugins {
    id("com.android.application")
}

android {
    namespace = "com.smanx.adbreeze"
    compileSdk = 36

    // 版本号由 CI 打 release 时注入（-PappVersion=x.y.z），本地构建用默认值
    val appVersion = (project.findProperty("appVersion") as String?)?.takeIf { it.isNotBlank() } ?: "1.0.0"
    // versionCode：x.y.z → x*10000 + y*100 + z；非三段式（如 dev 构建）回落 1
    val appVersionCode = appVersion.split(".").let { p ->
        val n = p.map { it.toIntOrNull() }
        if (p.size == 3 && n.all { it != null }) n[0]!! * 10000 + n[1]!! * 100 + n[2]!! else 1
    }

    defaultConfig {
        applicationId = "com.smanx.adbreeze"
        minSdk = 26
        targetSdk = 36
        versionCode = appVersionCode
        versionName = appVersion
    }

    // 正式签名：CI 通过环境变量注入（KEYSTORE_FILE 指向已解码的 keystore）；
    // 未注入时（本地无密钥）release 回落 debug 签名，保证本地仍可构建
    val releaseStoreFile = System.getenv("KEYSTORE_FILE")
    val releaseStorePassword = System.getenv("KEYSTORE_PASSWORD")
    val releaseKeyAlias = System.getenv("KEY_ALIAS") ?: "adbreeze"

    signingConfigs {
        if (releaseStoreFile != null && releaseStorePassword != null) {
            create("release") {
                storeFile = file(releaseStoreFile)
                storePassword = releaseStorePassword
                keyAlias = releaseKeyAlias
                keyPassword = releaseStorePassword
            }
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            signingConfig = if (releaseStoreFile != null && releaseStorePassword != null) {
                signingConfigs.getByName("release")
            } else {
                // 本地无密钥时回落 debug 签名
                signingConfigs.getByName("debug")
            }
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
}

// AGP 9 内建 Kotlin：jvmTarget 自动对齐 compileOptions，无需 kotlin {} 配置块

dependencies {
    // 零依赖：不引入 androidx，通知/WebView 全用平台 API（minSdk 26+）
}
