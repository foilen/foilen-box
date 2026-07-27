#!/usr/bin/env bash
set -euo pipefail

BUILD_TOOLS="$ANDROID_HOME/build-tools/36.0.0"

KEYS_DIR="_local_keys"
KEYSTORE="$KEYS_DIR/release-key.jks"
KEY_ALIAS="release-key"
STOREPASS="android"
KEYPASS="android"

APK_UNSIGNED="dist/android/Foilen_Box.apk"
APK_ALIGNED="$KEYS_DIR/foilen-box-android-release-aligned.apk"
APK_SIGNED="$KEYS_DIR/foilen-box-android-release-signed.apk"

# --- 1. Generate keystore if needed ---
if [ ! -f "$KEYSTORE" ]; then
    echo "No keystore found. Generating one in $KEYS_DIR/ ..."
    mkdir -p "$KEYS_DIR"
    keytool -genkeypair -v \
        -keystore "$KEYSTORE" \
        -storepass "$STOREPASS" \
        -keypass "$KEYPASS" \
        -keyalg RSA -keysize 2048 -validity 10000 \
        -alias "$KEY_ALIAS" \
        -dname "CN=Dev, OU=Dev, O=Dev, L=Dev, S=Dev, C=US"
    echo "Keystore created at $KEYSTORE"
else
    echo "Keystore already exists at $KEYSTORE"
fi

# --- 2. Align ---
echo "Aligning APK..."
"$BUILD_TOOLS/zipalign" -f -v 4 "$APK_UNSIGNED" "$APK_ALIGNED"

# --- 3. Sign ---
echo "Signing APK..."
"$BUILD_TOOLS/apksigner" sign \
    --ks "$KEYSTORE" \
    --ks-key-alias "$KEY_ALIAS" \
    --ks-pass "pass:$STOREPASS" \
    --key-pass "pass:$KEYPASS" \
    --out "$APK_SIGNED" \
    "$APK_ALIGNED"

# --- 4. Install ---
echo "Installing APK on connected device..."
adb install -r "$APK_SIGNED"

echo "Done."
