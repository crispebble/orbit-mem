#!/usr/bin/env bash
# 릴리스 자산을 dist/에 만든다: 버전은 internal/version 기준이다.
#   scripts/release.sh            빌드 + 체크섬만(발행하지 않음)
#   scripts/release.sh --publish  빌드 뒤 태그 vX.Y.Z로 GitHub Release를 올린다(깨끗한 origin/main 끝에서만)
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

VERSION="$(sed -n 's/.*Version *= *"\([^"]*\)".*/\1/p' internal/version/version.go | head -n1)"
[[ "$VERSION" =~ ^0\.[0-9]+\.[0-9]+$ ]] || { echo "버전을 읽지 못했거나 0.x가 아닙니다: '$VERSION'" >&2; exit 1; }
COMMIT="$(git rev-parse HEAD)"
DIST="dist/$VERSION"
rm -rf "$DIST"; mkdir -p "$DIST"

build() { # os arch ext
	local os=$1 arch=$2 name="orb-$VERSION-$1-$2" bin=orb
	[[ $os == windows ]] && bin=orb.exe
	local stage="$DIST/$name"; mkdir -p "$stage"
	GOOS=$os GOARCH=$arch CGO_ENABLED=0 go build -trimpath -o "$stage/$bin" ./cmd/orb
	cp README.md "$stage/"
	if [[ $os == windows ]]; then (cd "$DIST" && zip -qr "$name.zip" "$name"); else tar -C "$DIST" -czf "$DIST/$name.tar.gz" "$name"; fi
	rm -rf "$stage"
}
build darwin arm64
build darwin amd64
build windows amd64 # Windows는 빌드만 한다(설치 확인은 사용자가 지시할 때)
(cd "$DIST" && shasum -a 256 orb-* > SHA256SUMS)
echo "빌드 완료: $DIST (commit $COMMIT)"; cat "$DIST/SHA256SUMS"

[[ "${1:-}" == "--publish" ]] || exit 0
[[ -z "$(git status --porcelain)" ]] || { echo "작업 트리가 깨끗하지 않습니다." >&2; exit 1; }
git fetch -q origin main
[[ "$COMMIT" == "$(git rev-parse origin/main)" ]] || { echo "HEAD가 origin/main 끝이 아닙니다." >&2; exit 1; }
gh release create "v$VERSION" --target "$COMMIT" --title "OpenOrbit $VERSION" --notes-file "${RELEASE_NOTES:-/dev/null}" "$DIST"/orb-* "$DIST/SHA256SUMS"
