#!/bin/sh
# install.sh와 release.sh --check-versions 시험. 로컬 가짜 릴리스 + 임시 HTTP 서버만 쓴다(외부 네트워크 없음).
set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
INSTALL="$HERE/install.sh"
RELEASE="$HERE/release.sh"
ROOT="$(mktemp -d)"
SRV_PID=""
cleanup() { [ -n "$SRV_PID" ] && kill "$SRV_PID" 2>/dev/null; rm -rf "$ROOT"; }
trap cleanup EXIT INT TERM

PASS=0; FAIL=0
ok() { PASS=$((PASS + 1)); echo "ok   - $1"; }
bad() { FAIL=$((FAIL + 1)); echo "FAIL - $1"; }
check() { # 이름 조건(0이면 통과)
	if [ "$2" -eq 0 ]; then ok "$1"; else bad "$1"; fi
}

SHA() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$@"; else shasum -a 256 "$@"; fi; }

# 가짜 uname
mkdir -p "$ROOT/bin"
cat > "$ROOT/bin/uname" <<'U'
#!/bin/sh
case "${1:-}" in
-s) echo "${FAKE_OS:-Darwin}" ;;
-m) echo "${FAKE_ARCH:-arm64}" ;;
*) echo "${FAKE_OS:-Darwin}" ;;
esac
U
chmod +x "$ROOT/bin/uname"

# 가짜 릴리스: download/v<판>/
make_release() { # 판 os arch
	v=$1; os=$2; arch=$3
	name="orbit-mem-$v-$os-$arch"
	d="$ROOT/srv/download/v$v"
	mkdir -p "$d" "$ROOT/stage/$name"
	cat > "$ROOT/stage/$name/orbit-mem" <<'F'
#!/bin/sh
if [ "${1:-}" = "install" ]; then
	shift
	printf '%s\n' "$@" > "$FAKE_RECORD"
	exit 0
fi
exit 2
F
	chmod +x "$ROOT/stage/$name/orbit-mem"
	tar -C "$ROOT/stage" -czf "$d/$name.tar.gz" "$name"
	(cd "$d" && SHA "$name.tar.gz" >> SHA256SUMS)
}
make_release 1.2.3 darwin arm64
make_release 1.2.3 linux amd64
make_release 9.9.9 darwin arm64
mkdir -p "$ROOT/srv/api"
printf '{"tag_name": "v9.9.9", "name": "x"}\n' > "$ROOT/srv/api/latest"

PORT="$(python3 -I -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1])')"
python3 -I -m http.server "$PORT" --bind 127.0.0.1 --directory "$ROOT/srv" >/dev/null 2>&1 &
SRV_PID=$!
i=0
while ! python3 -I -c "import socket;socket.create_connection(('127.0.0.1',$PORT),1)" 2>/dev/null; do
	i=$((i + 1)); [ "$i" -gt 50 ] && { echo "서버가 뜨지 않았습니다" >&2; exit 2; }
	sleep 0.1
done

BASE="http://127.0.0.1:$PORT"
run_install() { # 인자... ; 환경: FAKE_OS FAKE_ARCH ORBIT_MEM_VERSION 등은 호출 쪽이 준다
	rm -rf "$ROOT/tmp" "$ROOT/home"; mkdir -p "$ROOT/tmp" "$ROOT/home"
	rm -f "$ROOT/record"
	HOME="$ROOT/home" TMPDIR="$ROOT/tmp" PATH="$ROOT/bin:$PATH" \
		ORBIT_MEM_RELEASES_URL="$BASE" ORBIT_MEM_LATEST_URL="$BASE/api/latest" \
		FAKE_RECORD="$ROOT/record" \
		sh "$INSTALL" "$@" >"$ROOT/out" 2>&1
}
tmp_clean() { [ -z "$(ls -A "$ROOT/tmp")" ]; }
home_clean() { [ -z "$(ls -A "$ROOT/home")" ]; }

# 1. 정상 설치: 최신 판, 인자 전달, 정리
( ORBIT_MEM_VERSION=1.2.3 run_install --no-hosts --foo bar ); rc=$?
[ -n "${KEEP_OUT:-}" ] && cat "$ROOT/out"
check "정상 설치 종료 코드 0" "$rc"
[ "$(cat "$ROOT/record" 2>/dev/null)" = "--no-hosts
--foo
bar" ]; check "인자를 orbit-mem install에 그대로 전달" $?
tmp_clean; check "성공 뒤 임시 폴더 정리" $?
home_clean; check "스크립트가 홈에 쓰지 않음" $?
grep -q "1.2.3" "$ROOT/out" && grep -q "$BASE" "$ROOT/out"; check "판과 출처를 출력" $?

# 2. 최신 판 조회
run_install --no-hosts; rc=$?
check "최신 판 설치 종료 코드 0" "$rc"
grep -q "9.9.9" "$ROOT/out"; check "tag_name의 v를 떼어 최신 판 9.9.9 사용" $?

# 3. --version (전달 인자에서 제외)
run_install --version 1.2.3 --no-hosts; rc=$?
check "--version 설치 종료 코드 0" "$rc"
[ "$(cat "$ROOT/record" 2>/dev/null)" = "--no-hosts" ]; check "--version은 install에 전달하지 않음" $?
grep -q "1.2.3" "$ROOT/out"; check "--version 판 사용" $?
run_install --version=1.2.3; rc=$?
check "--version=X 형식" "$rc"

# 4. Linux amd64
( FAKE_OS=Linux FAKE_ARCH=x86_64 ORBIT_MEM_VERSION=1.2.3 run_install ); rc=$?
check "Linux x86_64 설치" "$rc"

# 4b. 중간에서 끊긴 스크립트(curl | sh 전송 중단)는 아무것도 설치하지 않는다
for cut in 20 45 70 92; do
	head -n "$cut" "$INSTALL" > "$ROOT/install.cut.sh"
	rm -rf "$ROOT/tmp" "$ROOT/home"; mkdir -p "$ROOT/tmp" "$ROOT/home"; rm -f "$ROOT/record"
	HOME="$ROOT/home" TMPDIR="$ROOT/tmp" PATH="$ROOT/bin:$PATH" ORBIT_MEM_RELEASES_URL="$BASE" \
		ORBIT_MEM_VERSION=1.2.3 FAKE_RECORD="$ROOT/record" sh "$ROOT/install.cut.sh" >/dev/null 2>&1
	[ ! -e "$ROOT/record" ]; check "앞 ${cut}줄만 실행돼도 install을 부르지 않음" $?
done

# 5. 체크섬 불일치
sed 's/^[0-9a-f]\{4\}/0000/' "$ROOT/srv/download/v1.2.3/SHA256SUMS" > "$ROOT/sums.bad"
cp "$ROOT/srv/download/v1.2.3/SHA256SUMS" "$ROOT/sums.good"
cp "$ROOT/sums.bad" "$ROOT/srv/download/v1.2.3/SHA256SUMS"
( ORBIT_MEM_VERSION=1.2.3 run_install ); rc=$?
[ "$rc" -eq 1 ]; check "체크섬 불일치 종료 코드 1" $?
[ ! -e "$ROOT/record" ]; check "체크섬 불일치 시 install 호출 안 함" $?
tmp_clean; check "실패 뒤에도 임시 폴더 정리" $?

# 6. SHA256SUMS에 항목 없음
printf '%s  other-file.tar.gz\n' "0000000000000000000000000000000000000000000000000000000000000000" > "$ROOT/srv/download/v1.2.3/SHA256SUMS"
( ORBIT_MEM_VERSION=1.2.3 run_install ); rc=$?
[ "$rc" -eq 1 ]; check "SHA256SUMS 항목 없음 종료 코드 1" $?
[ ! -e "$ROOT/record" ]; check "항목 없음 시 install 호출 안 함" $?
cp "$ROOT/sums.good" "$ROOT/srv/download/v1.2.3/SHA256SUMS"

# 7. 지원하지 않는 조합
( FAKE_OS=FreeBSD FAKE_ARCH=amd64 ORBIT_MEM_VERSION=1.2.3 run_install ); rc=$?
[ "$rc" -eq 1 ]; check "지원하지 않는 OS 종료 코드 1" $?
[ -s "$ROOT/out" ]; check "지원하지 않는 이유를 출력" $?
[ ! -e "$ROOT/record" ]; check "미지원 조합은 install 호출 안 함" $?
( FAKE_OS=Darwin FAKE_ARCH=riscv64 ORBIT_MEM_VERSION=1.2.3 run_install ); rc=$?
[ "$rc" -eq 1 ]; check "지원하지 않는 아키텍처 종료 코드 1" $?

# 8. 없는 판
( ORBIT_MEM_VERSION=0.0.1 run_install ); rc=$?
[ "$rc" -ne 0 ]; check "없는 판은 실패" $?

# 9. release.sh --check-versions (VR-1)
mkv() { # 디렉터리 version.go plugin marketplace
	mkdir -p "$1/internal/version" "$1/plugin/.claude-plugin" "$1/.claude-plugin"
	printf 'package version\n\nvar Version = "%s"\n' "$2" > "$1/internal/version/version.go"
	printf '{\n  "name": "orbit-mem",\n  "version": "%s"\n}\n' "$3" > "$1/plugin/.claude-plugin/plugin.json"
	printf '{\n  "plugins": [\n    {\n      "name": "orbit-mem",\n      "version": "%s"\n    }\n  ]\n}\n' "$4" > "$1/.claude-plugin/marketplace.json"
}
mkv "$ROOT/v_ok" 0.13.0 0.13.0 0.13.0
bash "$RELEASE" --check-versions "$ROOT/v_ok" >"$ROOT/out" 2>&1; rc=$?
check "세 버전이 같으면 통과" "$rc"
mkv "$ROOT/v_bad" 0.13.0 0.13.0 0.12.0
bash "$RELEASE" --check-versions "$ROOT/v_bad" >"$ROOT/out" 2>&1; rc=$?
[ "$rc" -eq 1 ]; check "버전이 어긋나면 종료 코드 1" $?
grep -q "marketplace.json" "$ROOT/out" && grep -q "0.12.0" "$ROOT/out"; check "어긋난 파일과 값을 출력" $?
mkv "$ROOT/v_bad2" 0.14.0 0.13.0 0.13.0
bash "$RELEASE" --check-versions "$ROOT/v_bad2" >"$ROOT/out" 2>&1; rc=$?
[ "$rc" -eq 1 ]; check "version.go가 어긋나도 종료 코드 1" $?

[ -n "${KEEP_OUT:-}" ] && cat "$ROOT/out"
echo "통과 $PASS, 실패 $FAIL"
[ "$FAIL" -eq 0 ]
