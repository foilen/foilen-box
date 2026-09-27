plugins {
	id("com.android.application")
	id("org.jetbrains.kotlin.android")
}

android {
	namespace = "com.foilen.box.android"
	compileSdk = 36
	buildToolsVersion = "36.0.0"

	defaultConfig {
		applicationId = "com.foilen.box.android"
		minSdk = 26
		targetSdk = 36
		versionCode = 1
		versionName = "1.0"
	}

	buildTypes {
		release {
			isMinifyEnabled = false
		}
	}

	lint {
		checkReleaseBuilds = false
	}

	compileOptions {
		sourceCompatibility = JavaVersion.VERSION_17
		targetCompatibility = JavaVersion.VERSION_17
	}
	kotlinOptions {
		jvmTarget = "17"
	}
}

dependencies {
	implementation(files("libs/foilenbox.aar"))

	implementation("androidx.core:core-ktx:1.13.1")
	implementation("androidx.appcompat:appcompat:1.7.0")

	val cameraxVersion = "1.5.3"
	implementation("androidx.camera:camera-core:$cameraxVersion")
	implementation("androidx.camera:camera-camera2:$cameraxVersion")
	implementation("androidx.camera:camera-lifecycle:$cameraxVersion")

	implementation("androidx.lifecycle:lifecycle-service:2.8.7")

	implementation("androidx.work:work-runtime-ktx:2.10.0")
}
