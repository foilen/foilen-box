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
		// The Go AAR (built via `gomobile bind`) needs a modern enough minSdk;
		// bump this if gomobile's toolchain requires it.
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
		// AGP 8.7's lint tooling isn't compatible with compileSdk 36 yet
		// (fails with an opaque "25.0.4" error); the checks aren't needed
		// for local release builds.
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
	// Built by `gomobile bind -target=android -o android/app/libs/foilenbox.aar ./cmd/mobile`
	// (see ../../step-package.sh). Not committed to source control.
	implementation(files("libs/foilenbox.aar"))

	implementation("androidx.core:core-ktx:1.13.1")
	implementation("androidx.appcompat:appcompat:1.7.0")

	// CameraX, for CameraForegroundService.kt (native camera capture feeding
	// the Camera/RTSP feature): camera-core/-camera2/-lifecycle only —
	// capture is wired straight from a Preview use case into a MediaCodec
	// input Surface, no camera-video/ImageAnalysis needed.
	val cameraxVersion = "1.5.3"
	implementation("androidx.camera:camera-core:$cameraxVersion")
	implementation("androidx.camera:camera-camera2:$cameraxVersion")
	implementation("androidx.camera:camera-lifecycle:$cameraxVersion")

	// LifecycleService: lets CameraForegroundService be a LifecycleOwner so
	// CameraX can bindToLifecycle to it directly, without an Activity —
	// the whole point being that capture survives the screen turning off /
	// the app being backgrounded.
	implementation("androidx.lifecycle:lifecycle-service:2.8.7")
}
