#!/usr/bin/env bash
# Builds Stackfork.app (universal: arm64 and x86_64) and its DMG into
# dist/macos-app/ (add-macos-app D12). Needs a Mac with Xcode and Go.
#
#   scripts/build-macos-app.sh VERSION      e.g. 0.9.0 (no leading "v")
#
# The bundle is signed ad hoc with the hardened runtime. It is not notarized.
set -euo pipefail

VERSION="${1:?usage: scripts/build-macos-app.sh VERSION}"
case "$VERSION" in
  [0-9A-Za-z]*) ;;
  *) echo "VERSION must start with a letter or digit (no leading v): $VERSION" >&2; exit 2 ;;
esac
if [[ ! "$VERSION" =~ ^[0-9A-Za-z.+-]+$ ]]; then
  echo "VERSION has characters that do not belong in a version: $VERSION" >&2
  exit 2
fi

cd "$(dirname "$0")/.."
OUT=dist/macos-app
APP="$OUT/Stackfork.app"
NAME="stackfork-app_${VERSION}_darwin_universal"
DMG="$OUT/$NAME.dmg"
MODULE="$(go list -m)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

rm -rf "$OUT"
mkdir -p "$OUT"

echo "==> Go helper (universal)"
for arch in arm64 amd64; do
  GOOS=darwin GOARCH="$arch" CGO_ENABLED=0 go build \
    -ldflags "-s -w -X $MODULE/internal/cli.Version=$VERSION" \
    -o "$WORK/stackfork-$arch" ./cmd/stackfork
done
lipo -create -output "$WORK/stackfork" "$WORK/stackfork-arm64" "$WORK/stackfork-amd64"

echo "==> Swift app (universal)"
SWIFT_ARGS=(-c release --arch arm64 --arch x86_64 --package-path desktop/macos)
swift build "${SWIFT_ARGS[@]}"
APP_BIN="$(swift build "${SWIFT_ARGS[@]}" --show-bin-path)/Stackfork"

echo "==> Bundle"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Helpers" "$APP/Contents/Resources"
cp "$APP_BIN" "$APP/Contents/MacOS/Stackfork"
cp "$WORK/stackfork" "$APP/Contents/Helpers/stackfork"
cp desktop/macos/AppIcon.icns "$APP/Contents/Resources/AppIcon.icns"
sed "s/@VERSION@/$VERSION/g" desktop/macos/Info.plist > "$APP/Contents/Info.plist"
plutil -lint "$APP/Contents/Info.plist" >/dev/null

echo "==> Ad-hoc signature with the hardened runtime (helper first)"
codesign --force --sign - --options runtime "$APP/Contents/Helpers/stackfork"
codesign --force --sign - --options runtime "$APP"
codesign --verify --strict --verbose=2 "$APP"

echo "==> Checks"
for exe in "$APP/Contents/MacOS/Stackfork" "$APP/Contents/Helpers/stackfork"; do
  archs="$(lipo -archs "$exe")"
  for want in arm64 x86_64; do
    case " $archs " in
      *" $want "*) ;;
      *) echo "$exe lacks $want (has: $archs)" >&2; exit 1 ;;
    esac
  done
done
printed="$("$APP/Contents/Helpers/stackfork" version)"
if [ "${printed##* }" != "$VERSION" ]; then
  echo "helper prints '$printed', want version $VERSION" >&2
  exit 1
fi

echo "==> DMG"
STAGE="$WORK/dmg"
mkdir -p "$STAGE"
ditto "$APP" "$STAGE/Stackfork.app" # keeps the signature
ln -s /Applications "$STAGE/Applications"
hdiutil create -volname Stackfork -srcfolder "$STAGE" -format UDZO -ov "$DMG" >/dev/null
(cd "$OUT" && shasum -a 256 "$NAME.dmg" > "$NAME.dmg.sha256")

echo "Built:"
ls -l "$DMG" "$DMG.sha256"
