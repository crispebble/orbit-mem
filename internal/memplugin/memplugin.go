// Package memplugin is orbit-mem's part of the OpenOrbit plugin: the memory MCP server
// (orbit-mem mcp) and the three memory hooks (session start, prompt, session end), as a
// fragment that OpenOrbit's plugin build adds to the plugin (orb plugin build … --fragment F).
// The skills and roles are OpenOrbit's own source; building the plugin and registering it
// in the hosts is OpenOrbit's orb install.
package memplugin

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/onidot-labs/orbit-mem/internal/i18n"
)

// source is the fragment with {{mem}} (MCP command), {{memcmd}} (hook command lines) and
// {{host}} (filled per host by the build).
//
//go:embed fragment.json
var source []byte

// Vars are the fragment variables for an orbit-mem at path: {{mem}} for MCP commands and
// {{memcmd}} for hook command lines (quoted only when the path has spaces, so the same line
// works in sh, cmd and PowerShell).
func Vars(bin string) map[string]string {
	cmd := strings.ReplaceAll(bin, `\`, "/")
	if strings.ContainsAny(cmd, " \t") {
		cmd = `"` + cmd + `"`
	}
	return map[string]string{"mem": bin, "memcmd": cmd}
}

// Fragment is the fragment for an orbit-mem at path, as indented JSON. {{mem}} and {{memcmd}}
// are filled inside the decoded string values, so any path stays valid JSON; {{host}} is left
// for the build. Keys keep the source order (the build follows it).
func Fragment(bin string) ([]byte, error) {
	v := Vars(bin)
	r := strings.NewReplacer("{{memcmd}}", v["memcmd"], "{{mem}}", v["mem"])
	dec := json.NewDecoder(bytes.NewReader(source))
	dec.UseNumber()
	var compact bytes.Buffer
	if err := fill(dec, &compact, r); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err == nil {
		return nil, errors.New("memplugin: trailing data in fragment.json")
	}
	var out bytes.Buffer
	if err := json.Indent(&out, compact.Bytes(), "", "  "); err != nil {
		return nil, err
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
}

// fill copies one JSON value from dec to out, filling the placeholders in its strings.
func fill(dec *json.Decoder, out *bytes.Buffer, r *strings.Replacer) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	switch v := tok.(type) {
	case json.Delim:
		end := byte('}')
		if v == '[' {
			end = ']'
		}
		out.WriteByte(byte(v))
		for i := 0; dec.More(); i++ {
			if i > 0 {
				out.WriteByte(',')
			}
			if v == '{' {
				key, err := dec.Token()
				if err != nil {
					return err
				}
				if err := writeString(out, key.(string)); err != nil {
					return err
				}
				out.WriteByte(':')
			}
			if err := fill(dec, out, r); err != nil {
				return err
			}
		}
		if _, err := dec.Token(); err != nil { // closing delimiter
			return err
		}
		out.WriteByte(end)
	case string:
		return writeString(out, r.Replace(v))
	case json.Number:
		out.WriteString(v.String())
	case bool:
		if v {
			out.WriteString("true")
		} else {
			out.WriteString("false")
		}
	case nil:
		out.WriteString("null")
	}
	return nil
}

func writeString(out *bytes.Buffer, s string) error {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return err
	}
	out.Write(bytes.TrimSuffix(b.Bytes(), []byte("\n")))
	return nil
}

// Runner runs a command and returns its combined output.
type Runner func(name string, args ...string) ([]byte, error)

// RunCommand is the real Runner.
func RunCommand(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

// Build builds the OpenOrbit plugin from a plugin source into out (a marketplace folder) with
// this fragment for an orbit-mem at bin, through OpenOrbit's orb:
// orb plugin build <source> --out <out> --fragment <temporary file>.
func Build(orb, src, out, bin string, run Runner) error {
	b, err := Fragment(bin)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp("", "orbit-mem-fragment-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if run == nil {
		run = RunCommand
	}
	if msg, err := run(orb, "plugin", "build", src, "--out", out, "--fragment", f.Name()); err != nil {
		return fmt.Errorf("%s plugin build: %w: %s", orb, err, strings.TrimSpace(string(msg)))
	}
	return nil
}

// ErrNoOrb says that OpenOrbit's orb was not found.
var ErrNoOrb = i18n.Err("memplugin.err.no_orb")

// FindOrb finds OpenOrbit's orb: ORBIT_ORB, then orb on PATH, then ~/.local/bin/orb.
// ORBIT_ORB that points nowhere is an error of its own (no silent fallback).
func FindOrb() (string, error) {
	name := "orb"
	if runtime.GOOS == "windows" {
		name = "orb.exe"
	}
	if p := os.Getenv("ORBIT_ORB"); p != "" {
		if fi, err := os.Stat(p); err != nil || fi.IsDir() {
			return "", i18n.Errorf("memplugin.err.bad_orb", p)
		}
		return p, nil
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	if home, err := os.UserHomeDir(); err == nil {
		p := filepath.Join(home, ".local", "bin", name)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, nil
		}
	}
	return "", ErrNoOrb
}
