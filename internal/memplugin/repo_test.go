package memplugin

import (
	"strings"
	"testing"

	"github.com/onidot-labs/orbit-mem/internal/version"
)

// 끝 기준: 저장소에 커밋한 GitHub 마켓(.claude-plugin/marketplace.json, plugin/)이 내장 원본과 지금 판으로
// 만든 것과 바이트까지 같다. 다르면 go run ./cmd/orbit-mem plugin build --repo . 로 다시 만든다.
func TestRepo_커밋한_생성물이_원본과_같다(t *testing.T) {
	files, err := Repo(version.Version)
	if err != nil {
		t.Fatal(err)
	}
	if d := Diff("../..", files); len(d) > 0 {
		t.Fatalf("커밋한 플러그인이 원본과 어긋납니다. go run ./cmd/orbit-mem plugin build --repo . 로 다시 만드세요:\n%s", strings.Join(d, "\n"))
	}
}
