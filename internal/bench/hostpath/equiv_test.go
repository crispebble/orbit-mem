//go:build benchequiv

// 87ab0fb 판의 inject와 같음을 보는 증거 시험이다. 1-3·1-4를 합치면 깨지는 것이 정상이라(시그니처·사람 요청 판정)
// 기본 시험에서 뺐다. 합친 뒤에는 지우거나 새 판에 맞게 고친다(internal/bench/README.md 「1-3·1-4를 합친 뒤」).
package hostpath_test

import (
	"slices"
	"testing"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/inject"
	"github.com/crispebble/orbit-mem/internal/memory"
	"github.com/crispebble/orbit-mem/internal/project"
)

// 지금 판의 inject 결과와 같은 id를 낸다(host는 그 위의 얇은 층이다).
func TestRequest가_inject와_같은_id를_낸다(t *testing.T) {
	f := open(t)
	got, err := f.env.Recaller.Request("s-same", "/bench/app", "pnpm으로 설치해 줘")
	if err != nil {
		t.Fatal(err)
	}
	// 같은 사본을 한 번 더 복사해 inject를 직접 부른다(표시 표식이 섞이지 않게).
	db2, err := f.p.Copy()
	if err != nil {
		t.Fatal(err)
	}
	s, _, err := memory.OpenRead(db2)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := project.Resolve("/bench/app")
	where := p.Dir
	if where == "" {
		where = p.Scope
	}
	want := inject.PromptInjectionFor(s, config.Default(), where, "pnpm으로 설치해 줘", "bench", "s-same", inject.Options{})
	if !slices.Equal(got.IDs, want.MemoryIDs) {
		t.Fatalf("host ids = %v, inject ids = %v", got.IDs, want.MemoryIDs)
	}
}

// 사람이 아닌 글도 지금 판은 회상한다. 사람 요청 판정은 1-4가 붙인다.
func TestRequest는_사람이_아닌_글도_지금은_회상한다(t *testing.T) {
	f := open(t)
	got, err := f.env.Recaller.Request("s3", "/bench/app", "<task-notification>\n<summary> pnpm 설치가 끝났다 </summary>\n</task-notification>")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.IDs) == 0 {
		t.Fatalf("현재 판은 task-notification도 회상한다고 기록했는데 비었다: %+v", got)
	}
}
