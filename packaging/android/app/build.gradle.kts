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
		versionCode = 8
		versionName = "0.4.4"
	}

	// Signing.
	//
	// This was the debug key, on the reasoning that a sideloaded app with no
	// store to publish to needs no identity. That was wrong, and the way it
	// was wrong is the only way it could matter: Android generates the debug
	// key per machine, every CI run is a fresh machine, so every release was
	// signed with a different key and every update failed with "App not
	// installed as package conflicts with an existing package".
	//
	// The identity is not about a store. It is what makes one build an update
	// to the last one rather than a different app wearing its name.
	//
	// The key lives in the repository's secrets, not in the repository: this
	// one is public, and a signing key in it would let anyone build something
	// Android treats as an upgrade to Astral. A build without the secrets
	// still works and still installs; it just cannot upgrade a signed one.
	signingConfigs {
		create("release") {
			val store = System.getenv("ANDROID_KEYSTORE_PATH")
			if (!store.isNullOrBlank() && file(store).exists()) {
				storeFile = file(store)
				storePassword = System.getenv("ANDROID_KEYSTORE_PASSWORD")
				keyAlias = System.getenv("ANDROID_KEY_ALIAS")
				keyPassword = System.getenv("ANDROID_KEY_PASSWORD")
			}
		}
	}

	buildTypes {
		release {
			isMinifyEnabled = false
			val signed = signingConfigs.getByName("release")
			signingConfig = if (signed.storeFile != null) signed else signingConfigs.getByName("debug")
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
	// FileProvider, for handing the downloaded build to the system installer.
	implementation("androidx.core:core-ktx:1.15.0")
}
