#!/usr/bin/env bash
# Đóng gói lõi Go (../mobile) thành app/libs/trcfcore.aar cho app Android.
#
# Cần: Go, gomobile + gobind (`go install golang.org/x/mobile/cmd/gomobile@latest`
# và `.../cmd/gobind@latest`), Android SDK + NDK, JDK (dùng JDK đi kèm Android Studio).
# Chạy lại mỗi khi code Go đổi, TRƯỚC `./gradlew assembleRelease`.
set -euo pipefail
cd "$(dirname "$0")"

export ANDROID_HOME="${ANDROID_HOME:-$HOME/Library/Android/sdk}"
if [ -z "${ANDROID_NDK_HOME:-}" ]; then
  ANDROID_NDK_HOME="$(ls -d "$ANDROID_HOME"/ndk/* 2>/dev/null | sort -V | tail -1)"
  export ANDROID_NDK_HOME
fi
if [ -z "${JAVA_HOME:-}" ] && [ -d "/Applications/Android Studio.app/Contents/jbr/Contents/Home" ]; then
  export JAVA_HOME="/Applications/Android Studio.app/Contents/jbr/Contents/Home"
fi
export PATH="$PATH:$(go env GOPATH)/bin"

mkdir -p app/libs
(cd ../mobile && gomobile bind \
  -target=android/arm64,android/arm -androidapi 24 \
  -javapkg vn.trcf.bridge -trimpath -ldflags "-s -w" \
  -o ../android/app/libs/trcfcore.aar .)
echo "✓ app/libs/trcfcore.aar"
