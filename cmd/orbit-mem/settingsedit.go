package main

// 설정 파일 편집기(설계 · orbit-mem 2판 17.3): host-memory off·on이 Claude Code settings.json과 Codex
// config.toml의 값 몇 개만 바꾼다. 라이브러리 없이, 바꾸는 값 밖의 내용은 그대로 둔다.

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"

	"github.com/onidot-labs/orbit-mem/internal/i18n"
)

// TOML (no library: only bare top-level tables and key = value lines are needed). Lines
// keep their ending: a file written with CRLF stays CRLF.

// tomlKeyValue reads a key = value line: its key as written, the value, and what follows the value.
func tomlKeyValue(line string) (key, val, rest string, ok bool) {
	line = strings.TrimSuffix(line, "\r")
	k, v, ok := strings.Cut(line, "=")
	if !ok {
		return "", "", "", false
	}
	k = strings.TrimSpace(k)
	if k == "" || strings.HasPrefix(k, "#") || strings.HasPrefix(k, "[") {
		return "", "", "", false
	}
	v = strings.TrimLeft(v, " \t")
	val, rest = v, ""
	if i := tomlCommentAt(v); i >= 0 {
		val, rest = v[:i], v[i:]
	}
	trimmed := strings.TrimRight(val, " \t")
	if rest != "" {
		rest = val[len(trimmed):] + rest
	}
	return k, trimmed, rest, true
}

// tomlCommentAt is where a # comment starts in a value, outside quoted strings, or -1.
func tomlCommentAt(v string) int {
	var quote byte
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case quote == '"' && c == '\\':
			i++
		case quote != 0 && c == quote:
			quote = 0
		case quote != 0:
		case c == '"' || c == '\'':
			quote = c
		case c == '#':
			return i
		}
	}
	return -1
}

// tomlStrings are the quoted strings in a raw value (an array of strings, or one string).
func tomlStrings(raw string) []string {
	var out []string
	for i := 0; i < len(raw); i++ {
		q := raw[i]
		if q != '"' && q != '\'' {
			continue
		}
		var b strings.Builder
		for i++; i < len(raw) && raw[i] != q; i++ {
			if q == '"' && raw[i] == '\\' && i+1 < len(raw) {
				i++
			}
			b.WriteByte(raw[i])
		}
		out = append(out, b.String())
	}
	return out
}

func tomlHeader(line string) (string, bool) {
	t := strings.TrimSpace(line)
	if i := strings.Index(t, "#"); i >= 0 && strings.Contains(t[:i], "]") {
		t = strings.TrimSpace(t[:i])
	}
	if strings.HasPrefix(t, "[") && !strings.HasPrefix(t, "[[") && strings.HasSuffix(t, "]") {
		return strings.TrimSpace(t[1 : len(t)-1]), true
	}
	return "", false
}

// tomlRootDotted is the last root line with a table.key key, or -1.
func tomlRootDotted(lines []string, table string) int {
	last := -1
	for i, l := range lines {
		if _, ok := tomlHeader(l); ok {
			break
		}
		if k, _, _, ok := tomlKeyValue(l); ok && strings.HasPrefix(k, table+".") {
			last = i
		}
	}
	return last
}

// tomlFind is the line index of table.key (under [table], or as table.key before any
// table), and the index of the [table] header (-1 when none).
func tomlFind(lines []string, table, key string) (at, header int) {
	at, header = -1, -1
	section := ""
	for i, l := range lines {
		if h, ok := tomlHeader(l); ok {
			section = h
			if h == table && header < 0 {
				header = i
			}
			continue
		}
		k, _, _, ok := tomlKeyValue(l)
		if !ok {
			continue
		}
		if (section == table && k == key) || (section == "" && k == table+"."+key) {
			return i, header
		}
	}
	return -1, header
}

// tomlTable is one [header] table with its key = value lines (raw values).
type tomlTable struct {
	Header string
	Keys   map[string]string
}

// tomlTables lists the [header] tables of a file in order. An array value written over
// several lines is gathered into one raw value.
func tomlTables(text string) []tomlTable {
	var ts []tomlTable
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		if h, ok := tomlHeader(lines[i]); ok {
			ts = append(ts, tomlTable{Header: h, Keys: map[string]string{}})
			continue
		}
		k, v, _, ok := tomlKeyValue(lines[i])
		if !ok || len(ts) == 0 {
			continue
		}
		for strings.HasPrefix(v, "[") && strings.Count(v, "[") > strings.Count(v, "]") && i+1 < len(lines) {
			i++
			next := strings.TrimSpace(strings.TrimSuffix(lines[i], "\r"))
			if c := tomlCommentAt(next); c >= 0 {
				next = strings.TrimSpace(next[:c])
			}
			v += " " + next
		}
		ts[len(ts)-1].Keys[k] = v
	}
	return ts
}

// unquoteTOML is a key without its quotes ("orbit-mem@orbit-mem" → orbit-mem@orbit-mem).
func unquoteTOML(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

// tomlInline says whether table is written as an inline table at the root (table = { … }),
// which these line edits cannot change without breaking the file.
func tomlInline(text, table string) bool {
	for _, l := range strings.Split(text, "\n") {
		if _, ok := tomlHeader(l); ok {
			return false
		}
		if k, v, _, ok := tomlKeyValue(l); ok && k == table && strings.HasPrefix(v, "{") {
			return true
		}
	}
	return false
}

func tomlGet(text, table, key string) (string, bool) {
	lines := strings.Split(text, "\n")
	at, _ := tomlFind(lines, table, key)
	if at < 0 {
		return "", false
	}
	_, v, _, _ := tomlKeyValue(lines[at])
	return v, true
}

// tomlSet sets table.key to val (a raw TOML value) or removes it (val nil), leaving every
// other line as it was. A missing key goes after the last line of its table; a missing
// table goes at the end.
func tomlSet(text, table, key string, val *string) string {
	cr := ""
	if strings.Contains(text, "\r\n") {
		cr = "\r"
	}
	lines := strings.Split(text, "\n")
	at, header := tomlFind(lines, table, key)
	switch {
	case at >= 0 && val == nil:
		lines = slices.Delete(lines, at, at+1)
	case at >= 0:
		l := lines[at]
		eq := strings.Index(l, "=")
		_, _, rest, _ := tomlKeyValue(l)
		end := ""
		if strings.HasSuffix(l, "\r") {
			end = "\r"
		}
		lines[at] = strings.TrimRight(l[:eq], " \t") + " = " + *val + rest + end
	case val == nil:
	case header < 0 && tomlRootDotted(lines, table) >= 0:
		// 표가 점 키로만 정의돼 있으면 [표]를 새로 열 수 없다(TOML이 금지): 점 키로 더한다.
		lines = slices.Insert(lines, tomlRootDotted(lines, table)+1, table+"."+key+" = "+*val+cr)
	case header >= 0:
		last := header
		for i := header + 1; i < len(lines); i++ {
			if _, ok := tomlHeader(lines[i]); ok {
				break
			}
			if strings.TrimSpace(lines[i]) != "" {
				last = i
			}
		}
		lines = slices.Insert(lines, last+1, key+" = "+*val+cr)
	default:
		body := strings.TrimRight(text, "\r\n")
		block := "[" + table + "]" + cr + "\n" + key + " = " + *val + cr + "\n"
		if body == "" {
			return block
		}
		return body + cr + "\n" + cr + "\n" + block
	}
	return strings.Join(lines, "\n")
}

// JSON with the members kept in their order (Claude Code's settings.json).

type jsonMember struct {
	Key string
	Val json.RawMessage
}

func parseObject(b []byte) ([]jsonMember, error) {
	if len(bytes.TrimSpace(b)) == 0 {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, i18n.Err("cli.hostmem.err.not_object")
	}
	var ms []jsonMember
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := t.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		ms = append(ms, jsonMember{key, raw})
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	return ms, nil
}

func compactObject(ms []jsonMember) []byte {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, m := range ms {
		if i > 0 {
			b.WriteByte(',')
		}
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(m.Key)
		b.Truncate(b.Len() - 1) // Encode adds a newline
		b.WriteByte(':')
		_ = json.Compact(&b, m.Val)
	}
	b.WriteByte('}')
	return b.Bytes()
}

func setMember(ms []jsonMember, path []string, val *string) ([]jsonMember, error) {
	i := slices.IndexFunc(ms, func(m jsonMember) bool { return m.Key == path[0] })
	if len(path) == 1 {
		switch {
		case val == nil && i >= 0:
			return slices.Delete(ms, i, i+1), nil
		case val == nil:
			return ms, nil
		case i >= 0:
			ms[i].Val = json.RawMessage(*val)
			return ms, nil
		}
		return append(ms, jsonMember{path[0], json.RawMessage(*val)}), nil
	}
	var inner []jsonMember
	if i >= 0 {
		var err error
		if inner, err = parseObject(ms[i].Val); err != nil {
			return nil, err
		}
	} else if val == nil {
		return ms, nil
	}
	inner, err := setMember(inner, path[1:], val)
	if err != nil {
		return nil, err
	}
	if i >= 0 {
		ms[i].Val = compactObject(inner)
		return ms, nil
	}
	return append(ms, jsonMember{path[0], compactObject(inner)}), nil
}

// jsonIndentOf is the indent of the first indented line, else two spaces.
func jsonIndentOf(b []byte) string {
	for _, l := range strings.Split(string(b), "\n")[1:] {
		if t := strings.TrimLeft(l, " \t"); t != "" && len(t) < len(l) {
			return l[:len(l)-len(t)]
		}
	}
	return "  "
}

func jsonGet(b []byte, path []string) (string, bool) {
	raw := json.RawMessage(b)
	for _, k := range path {
		ms, err := parseObject(raw)
		if err != nil {
			return "", false
		}
		i := slices.IndexFunc(ms, func(m jsonMember) bool { return m.Key == k })
		if i < 0 {
			return "", false
		}
		raw = ms[i].Val
	}
	var c bytes.Buffer
	if json.Compact(&c, raw) != nil {
		return "", false
	}
	return c.String(), true
}

// jsonSet sets the value at path to val (raw JSON) or removes it (val nil). The members
// keep their order and the file its indent unit.
func jsonSet(b []byte, path []string, val *string) ([]byte, error) {
	ms, err := parseObject(b)
	if err != nil {
		return nil, err
	}
	if ms, err = setMember(ms, path, val); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, compactObject(ms), "", jsonIndentOf(b)); err != nil {
		return nil, err
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
}
