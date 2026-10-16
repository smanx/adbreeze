pluginManagement {
    repositories {
        google()
        mavenCentral()
        gradlePluginPortal()
    }
}

dependencyResolutionManagement {
    repositoriesMode.set(RepositoriesMode.FAIL_ON_PROJECT_REPOS)
    repositories {
        google()
        mavenCentral()
    }
}

plugins {
    // AGP 9.0 起 Kotlin 支持内建，不再（也不能）应用 org.jetbrains.kotlin.android
    id("com.android.application") version "9.1.0" apply false
}

rootProject.name = "adbreeze"
include(":app")
