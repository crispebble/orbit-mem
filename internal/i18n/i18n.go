// Package i18n holds the messages orbit-mem shows people, in English (the default)
// and Korean (명세: Atlas ONID-61). Messages are kept by key in
// locales/<lang>/*.json; every language has the same keys.
//
// Only what a person reads is here: command output, errors and help. JSON
// output, MCP response structures, text written for the AI (injection,
// retrospective prompts, tool descriptions) and memory content are not.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"
	"sync/atomic"
)

// Lang is a supported language.
type Lang string

const (
	EN Lang = "en"
	KO Lang = "ko"
)

// Langs are the supported languages; English is the default.
var Langs = []Lang{EN, KO}

// EnvVar picks the language before the setting does.
const EnvVar = "ORBIT_LANG"

// DynamicPrefixes are key prefixes the code completes with a value (for
// example a memory kind), so the keys do not appear literally in the code.
var DynamicPrefixes = []string{"usage.", "kind.", "state.", "verdict.", "target."}

//go:embed locales
var files embed.FS

var (
	catalogs = map[Lang]map[string]string{}
	loadErrs []string
	current  atomic.Value // Lang
)

func init() {
	for _, l := range Langs {
		catalogs[l] = map[string]string{}
		dir := path.Join("locales", string(l))
		entries, err := files.ReadDir(dir)
		if err != nil {
			loadErrs = append(loadErrs, err.Error())
			continue
		}
		for _, e := range entries {
			b, err := files.ReadFile(path.Join(dir, e.Name()))
			if err != nil {
				loadErrs = append(loadErrs, err.Error())
				continue
			}
			var m map[string]string
			if err := json.Unmarshal(b, &m); err != nil {
				loadErrs = append(loadErrs, fmt.Sprintf("%s/%s: %v", dir, e.Name(), err))
				continue
			}
			for k, v := range m {
				if _, dup := catalogs[l][k]; dup {
					loadErrs = append(loadErrs, fmt.Sprintf("%s: key %s is in two files", dir, k))
				}
				catalogs[l][k] = v
			}
		}
	}
	current.Store(EN)
}

// Parse reads a language name such as "ko", "KO" or "ko_KR.UTF-8".
func Parse(s string) (Lang, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if i := strings.IndexAny(s, "-_."); i >= 0 {
		s = s[:i]
	}
	for _, l := range Langs {
		if s == string(l) {
			return l, true
		}
	}
	return "", false
}

// Resolve picks the language: the environment variable, then the setting,
// then English.
func Resolve(env, setting string) Lang {
	if l, ok := Parse(env); ok {
		return l
	}
	if l, ok := Parse(setting); ok {
		return l
	}
	return EN
}

// Set sets the language of this process.
func Set(l Lang) { current.Store(l) }

// Current is the language of this process.
func Current() Lang { return current.Load().(Lang) }

func lookup(key string) string { return lookupIn(Current(), key) }

func lookupIn(l Lang, key string) string {
	if m, ok := catalogs[l][key]; ok {
		return m
	}
	if m, ok := catalogs[EN][key]; ok {
		return m
	}
	return key
}

// T is the message of key in the current language, formatted with args.
func T(key string, args ...any) string { return TIn(Current(), key, args...) }

// TIn is the message of key in language l, for text whose language is fixed
// whatever the person picked (for example material written for the AI).
func TIn(l Lang, key string, args ...any) string {
	m := lookupIn(l, key)
	if len(args) == 0 && !strings.Contains(m, "%") {
		return m
	}
	return fmt.Sprintf(m, args...)
}

// Errorf is an error with the message of key; %w wraps as in fmt.Errorf.
func Errorf(key string, args ...any) error {
	return fmt.Errorf(lookup(key), args...)
}

// Error is a fixed error whose message is read in the language of the moment,
// so package-level sentinels made before the language is set still follow it.
type Error struct{ Key string }

func (e *Error) Error() string { return lookup(e.Key) }

// Err is a fixed error with the message of key.
func Err(key string) error { return &Error{Key: key} }

var verbRe = regexp.MustCompile(`%%|%[-+# 0]*(\d+|\*)?(\.(\d+|\*))?[a-zA-Z]`)

// Verbs are the format verbs of a message in order, without %%.
func Verbs(msg string) []string {
	var out []string
	for _, v := range verbRe.FindAllString(msg, -1) {
		if v != "%%" {
			out = append(out, v)
		}
	}
	return out
}
