package contract

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/crispebble/orbit-mem/internal/host"
)

func TestHostV1_짝_파일을_모두_읽고_이름이_겹치지_않는다(t *testing.T) {
	pairs, err := HostV1()
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) == 0 {
		t.Fatal("no pairs")
	}
	seen := map[string]bool{}
	for _, p := range pairs {
		if p.Name == "" || p.Op == "" {
			t.Errorf("pair without name or op: %+v", p)
		}
		if seen[p.Name] {
			t.Errorf("duplicate pair name %q", p.Name)
		}
		seen[p.Name] = true
		if !slices.Contains([]string{"", "none", "empty", "memories"}, p.Setup.DB) {
			t.Errorf("%s: unknown setup.db %q", p.Name, p.Setup.DB)
		}
		if p.Output == nil {
			t.Errorf("%s: no output", p.Name)
		}
	}
}

func TestHostV1_op_여덟을_모두_덮는다(t *testing.T) {
	pairs, err := HostV1()
	if err != nil {
		t.Fatal(err)
	}
	covered := map[string]bool{}
	for _, p := range pairs {
		covered[p.Op] = true
	}
	for _, op := range host.Ops {
		if !covered[op] {
			t.Errorf("no pair for op %s", op)
		}
	}
}

func TestHostV1_실패_시간_초과_판_불일치_짝이_있다(t *testing.T) {
	pairs, err := HostV1()
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]bool{}
	for _, p := range pairs {
		if m, ok := p.Output.(map[string]any); ok {
			if c, ok := m["code"].(string); ok {
				codes[c] = true
			}
		}
	}
	for _, c := range []string{host.CodeUsage, host.CodeContract, host.CodeTimeout, host.CodeNoDB, host.CodeSchemaNewer, host.CodeUnknownTool} {
		if !codes[c] {
			t.Errorf("no pair with code %s", c)
		}
	}
}

func TestMatch_자리표와_정확한_값(t *testing.T) {
	parse := func(s string) any {
		var v any
		if err := json.Unmarshal([]byte(s), &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	ok := []struct{ want, got string }{
		{`{"a":1,"b":"x"}`, `{"b":"x","a":1}`},
		{`{"t":"$string","n":"$number","l":"$array","z":"$any"}`, `{"t":"","n":3.5,"l":[],"z":{"k":1}}`},
		{`{"l":["$string",2]}`, `{"l":["q",2]}`},
		{`"$string"`, `"$x"`}, // 자리표는 문자열 자리에서만: 값이 문자열이면 맞는다
		{`{"t":"$nonEmptyString","l":"$nonEmptyArray"}`, `{"t":"x","l":["m-1"]}`},
		{`{"t":"$nonEmptyString"}`, `{"t":" a "}`},
	}
	for _, c := range ok {
		if err := Match(parse(c.want), parse(c.got)); err != nil {
			t.Errorf("Match(%s, %s) = %v", c.want, c.got, err)
		}
	}
	bad := []struct{ want, got, why string }{
		{`{"a":1}`, `{"a":2}`, "value"},
		{`{"a":1}`, `{"a":1,"b":2}`, "extra key"},
		{`{"a":1,"b":2}`, `{"a":1}`, "missing key"},
		{`{"t":"$string"}`, `{"t":5}`, "placeholder type"},
		{`{"n":"$number"}`, `{"n":"5"}`, "placeholder type"},
		{`{"l":"$array"}`, `{"l":{}}`, "placeholder type"},
		{`{"z":"$any"}`, `{}`, "any needs the key"},
		{`{"l":[1,2]}`, `{"l":[1]}`, "array length"},
		{`{"t":"$nonEmptyString"}`, `{"t":""}`, "empty string"},
		{`{"t":"$nonEmptyString"}`, `{"t":"  \n"}`, "blank string"},
		{`{"t":"$nonEmptyString"}`, `{"t":1}`, "placeholder type"},
		{`{"l":"$nonEmptyArray"}`, `{"l":[]}`, "empty array"},
		{`{"l":"$nonEmptyArray"}`, `{"l":"m-1"}`, "placeholder type"},
		{`{"t":"$nonEmpty"}`, `{"t":"x"}`, "unknown placeholder is exact"},
	}
	for _, c := range bad {
		if err := Match(parse(c.want), parse(c.got)); err == nil {
			t.Errorf("Match(%s, %s) passed (%s)", c.want, c.got, c.why)
		} else if !strings.Contains(err.Error(), "$") && c.why == "value" {
			t.Errorf("error without the path: %v", err)
		}
	}
}

func TestResolve_앞선_호출_결과를_가리키는_자리표(t *testing.T) {
	calls := []any{
		map[string]any{"name": "memory_save", "result": map[string]any{"memory": map[string]any{"id": "m-1"}, "list": []any{"a", "b"}}},
	}
	in := map[string]any{"args": map[string]any{"ids": []any{"$calls[0].result.memory.id"}, "x": "$calls[0].result.list[1]", "plain": "$string", "n": 3.0}}
	got, err := Resolve(in, calls)
	if err != nil {
		t.Fatal(err)
	}
	args := got.(map[string]any)["args"].(map[string]any)
	if args["ids"].([]any)[0] != "m-1" || args["x"] != "b" || args["plain"] != "$string" || args["n"] != 3.0 {
		t.Fatalf("resolved = %v", got)
	}
	for _, bad := range []string{"$calls[1].name", "$calls[0].result.nope", "$calls[0].result.list[5]", "$calls[0].result.memory.id.x", "$calls[0].name[0]"} {
		if _, err := Resolve(map[string]any{"v": bad}, calls); err == nil {
			t.Errorf("%s resolved", bad)
		}
	}
}

func TestHostV1_자리표는_앞선_호출만_가리킨다(t *testing.T) {
	pairs, err := HostV1()
	if err != nil {
		t.Fatal(err)
	}
	var uses int
	for _, p := range pairs {
		if strings.Contains(string(p.Input), "$calls") {
			uses++
			if len(p.Setup.Calls) == 0 {
				t.Errorf("%s: $calls without setup.calls", p.Name)
			}
		}
	}
	if uses < 2 {
		t.Errorf("pairs using $calls = %d", uses)
	}
}

// 짝 파일 옆 README가 TypeScript 쪽이 Go 없이 짝을 재현하는 데 필요한 것을 적는다(검토 M4): 모든 자리표,
// setup.db 값마다 만드는 법, meta·config가 쓰이는 곳, 종료 코드.
func TestHostV1_README가_짝_꼴과_자리표와_setup을_적는다(t *testing.T) {
	b, err := os.ReadFile("host/v1/README.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	for _, want := range append(slices.Clone(Placeholders),
		"$calls[N]", `"none"`, `"empty"`, `"memories"`, "orbit-mem migrate", "meta(key, value)", "$ORBIT_HOME/config.json",
		"mayHaveApplied", "`0`", "`1`", "`2`") {
		if !strings.Contains(doc, want) {
			t.Errorf("README에 %q가 없다", want)
		}
	}
	for _, p := range Placeholders {
		if err := Match(p, "x"); p != "$number" && p != "$array" && p != "$nonEmptyArray" && err != nil {
			t.Errorf("%s가 Match에서 자리표가 아니다: %v", p, err)
		}
	}
}

// 기억이 있는 짝(setup.db memories)의 회상 결과는 건너뛴 까닭(skipped)이 없으면 빈 결과로 통과하지 않는다: 글과 memoryIds가 비지 않아야 한다(검토 M4).
func TestHostV1_기억이_있는_회상_짝은_실제_주입을_요구한다(t *testing.T) {
	pairs, err := HostV1()
	if err != nil {
		t.Fatal(err)
	}
	var n int
	for _, p := range pairs {
		m, _ := p.Output.(map[string]any)
		if p.Setup.DB != "memories" || (p.Op != host.OpContext && p.Op != host.OpRecall) || p.Exit != 0 || m["skipped"] != nil {
			continue
		}
		n++
		if m["text"] != "$nonEmptyString" || m["memoryIds"] != "$nonEmptyArray" {
			t.Errorf("%s: text %v memoryIds %v", p.Name, m["text"], m["memoryIds"])
		}
	}
	if n < 3 {
		t.Fatalf("기억이 있는 회상 짝 = %d", n)
	}
}

// 쓰기 도구의 timeout 짝이 mayHaveApplied를 고정한다(검토 M3).
func TestHostV1_쓰기_도구_timeout_짝이_있다(t *testing.T) {
	pairs, err := HostV1()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pairs {
		if m, ok := p.Output.(map[string]any); ok && m["code"] == host.CodeTimeout && m["mayHaveApplied"] == true {
			return
		}
	}
	t.Fatal("no write tool timeout pair with mayHaveApplied")
}
