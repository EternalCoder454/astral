plugins {
	id("com.android.application")
	id("org.jetbrains.kotlin.android")
}

android {
	namespace = "io.github.astral"
	compileSdk = 35

	defaultConfig {
		applicationId = "io.github.astral"
		// Android 7 and up. Below that the WebView is too old to be worth
		// testing against, and the phone is too old to be on a network with a
		// machine running a 27B model.
		minSdk = 24
		targetSdk = 35
		versionCode = 2
		versionName = "0.2.0"
	}

	buildTypes {
		release {
			isMinifyEnabled = false
			// Signed with the debug key on purpose. This is a sideloaded app
			// for one person's own phone, talking to one person's own PC;
			// there is no store to publish to and no signing identity worth
			// inventing for it.
			signingConfig = signingConfigs.getByName("debug")
		}
	}

	compileOptions {
		sourceCompatibility = JavaVersion.VERSION_17
		targetCompatibility = JavaVersion.VERSION_17
	}
	kotlinOptions { jvmTarget = "17" }
}

dependencies {
	implementation("androidx.appcompat:appcompat:1.7.0")
}
