#!/bin/sh
# orbit-mem 설치기: 릴리스를 받아 SHA256을 검증한 뒤 `orbit-mem install`에 넘긴다.
#   curl -fsSL https://raw.githubusercontent.com/onidot-labs/orbit-mem/main/scripts/install.sh | sh
#   curl -fsSL .../install.sh | sh -s -- --no-hosts
# 환경 변수: ORBIT_MEM_VERSION(판), ORBIT_MEM_RELEASES_URL(다운로드 베이스), ORBIT_MEM_LATEST_URL(최신 판 조회).
# (바이너리 `orbit-mem update`가 읽는 ORBIT_MEM_BASE_URL과 뜻이 달라 이름을 나눴다.)
# 이 스크립트는 홈 디렉터리에 쓰지 않는다. 설치는 `orbit-mem install`이 한다.
#
# 본문 전체가 main() 안에 있고 마지막 줄에서만 호출한다: `curl | sh`가 중간에서 끊겨도
# 검증 앞뒤가 잘린 채 일부만 실행되지 않는다.
set -eu

die() { echo "orbit-mem 설치 실패: $*" >&2; exit 1; }

main() {
	BASE_URL="${ORBIT_MEM_RELEASES_URL:-https://github.com/onidot-labs/orbit-mem/releases}"
	LATEST_URL="${ORBIT_MEM_LATEST_URL:-https://api.github.com/repos/onidot-labs/orbit-mem/releases/latest}"
	VERSION="${ORBIT_MEM_VERSION:-}"

	# --version 만 가려내고 나머지는 install에 그대로 전달한다.
	n=$#
	while [ "$n" -gt 0 ]; do
		arg=$1; shift; n=$((n - 1))
		case "$arg" in
		--version)
			[ "$n" -gt 0 ] || die "--version 에 판이 없습니다"
			VERSION=$1; shift; n=$((n - 1)) ;;
		--version=*) VERSION=${arg#--version=} ;;
		*) set -- "$@" "$arg" ;;
		esac
	done

	case "$(uname -s)" in
	Darwin) OS=darwin ;;
	Linux) OS=linux ;;
	*) die "지원하지 않는 OS입니다: $(uname -s) (macOS, Linux만 지원)" ;;
	esac
	case "$(uname -m)" in
	arm64 | aarch64) ARCH=arm64 ;;
	x86_64 | amd64) ARCH=amd64 ;;
	*) die "지원하지 않는 CPU입니다: $(uname -m) (arm64, amd64만 지원)" ;;
	esac

	command -v curl >/dev/null 2>&1 || die "curl이 필요합니다"
	if command -v sha256sum >/dev/null 2>&1; then
		hash_of() { sha256sum "$1" | awk '{print $1}'; }
	elif command -v shasum >/dev/null 2>&1; then
		hash_of() { shasum -a 256 "$1" | awk '{print $1}'; }
	else
		die "sha256sum 또는 shasum이 필요합니다"
	fi

	if [ -z "$VERSION" ]; then
		echo "최신 판을 조회합니다: $LATEST_URL"
		VERSION="$(curl -fsSL "$LATEST_URL" | sed -n 's/.*"tag_name"[^"]*"\([^"]*\)".*/\1/p' | head -n1)" || true
		[ -n "$VERSION" ] || die "최신 판을 읽지 못했습니다 (GitHub API 요청 한도에 걸렸을 수 있습니다. 판을 직접 정하려면 ORBIT_MEM_VERSION=0.13.0 처럼 지정하세요)"
	fi
	VERSION=${VERSION#v}
	[ -n "$VERSION" ] || die "판 번호가 비어 있습니다"
	case "$VERSION" in # URL에 들어가므로 숫자와 점만 받는다
	*[!0-9.]* | .* | *. | *..*) die "판 번호 형식이 올바르지 않습니다: $VERSION (예: 0.13.0)" ;;
	esac

	NAME="orbit-mem-$VERSION-$OS-$ARCH"
	FILE="$NAME.tar.gz"
	URL_DIR="$BASE_URL/download/v$VERSION"

	WORK="$(mktemp -d "${TMPDIR:-/tmp}/orbit-mem-install.XXXXXX")"
	trap 'rm -rf "$WORK"' EXIT
	trap 'exit 1' INT TERM HUP

	echo "orbit-mem $VERSION ($OS/$ARCH)을 받습니다: $URL_DIR/$FILE"
	curl -fsSL -o "$WORK/$FILE" "$URL_DIR/$FILE" || die "$FILE 을 받지 못했습니다"
	curl -fsSL -o "$WORK/SHA256SUMS" "$URL_DIR/SHA256SUMS" || die "SHA256SUMS를 받지 못했습니다"

	want="$(awk -v f="$FILE" '$2 == f || $2 == "*" f {print $1; exit}' "$WORK/SHA256SUMS")"
	[ -n "$want" ] || die "SHA256SUMS에 $FILE 항목이 없습니다"
	got="$(hash_of "$WORK/$FILE")"
	[ "$want" = "$got" ] || die "SHA256이 일치하지 않습니다 ($FILE): 기대 $want, 실제 $got"
	echo "SHA256 확인: $got"

	tar -C "$WORK" -xzf "$WORK/$FILE" || die "압축을 풀지 못했습니다"
	BIN="$WORK/$NAME/orbit-mem"
	[ -f "$BIN" ] || BIN="$WORK/orbit-mem"
	[ -f "$BIN" ] || die "압축 안에 orbit-mem 실행 파일이 없습니다"
	chmod +x "$BIN"

	# exec 하면 trap이 돌지 않으므로 호출 뒤 종료 코드를 받아 정리한다.
	rc=0
	"$BIN" install "$@" || rc=$?
	exit "$rc"
}

main "$@"
