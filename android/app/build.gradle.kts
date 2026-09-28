import org.jetbrains.kotlin.gradle.dsl.JvmTarget

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

android {
    namespace = "vn.trcf.bridge.app"
    compileSdk = 34

    defaultConfig {
        applicationId = "vn.trcf.bridge"
        minSdk = 24
        targetSdk = 34
        versionCode = 1
        versionName = "0.1.0"
        // Lõi Go đóng gói cho 2 kiến trúc ARM (xem build-core.sh) — máy POS và emulator
        // Apple Silicon đều thuộc nhóm này.
        ndk { abiFilters += listOf("arm64-v8a", "armeabi-v7a") }
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            // Ký bằng khoá debug để người lắp cài được ngay một file APK, không cần kho
            // khoá riêng. Đổi sang khoá phát hành khi có kênh cập nhật chính thức.
            signingConfig = signingConfigs.getByName("debug")
        }
    }

    buildFeatures { buildConfig = true }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
}

kotlin {
    compilerOptions { jvmTarget.set(JvmTarget.JVM_17) }
}

dependencies {
    // Lõi cầu nối (Go) — `../build-core.sh` sinh ra từ ../mobile.
    implementation(files("libs/trcfcore.aar"))
}
