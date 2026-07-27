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
}
