// Package memplugin is orb's part of the OpenOrbit plugin (설계 · OpenOrbit 4차 「구조
// (2026-11-01 확정)」): the memory MCP server and the hooks, as a fragment the OpenOrbit
// harness build adds to the plugin (openorbit-harness build … --fragment F). The skills and
// roles are the harness's own source.
package memplugin

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/onidot-labs/orbit-mem/internal/openorbit"
)

// source is the fragment with {{orb}} (MCP command), {{orbcmd}} (hook command lines) and
// {{host}} (filled per host by the build).
//
//go:embed fragment.json
var source []byte

// Vars are the build variables for an orb at path: {{orb}} for MCP commands and {{orbcmd}}
// for hook command lines (quoted only when the path has spaces, so the same line works in
// sh, cmd and PowerShell).
func Vars(orb string) map[string]string {
	cmd := strings.ReplaceAll(orb, `\`, "/")
	if strings.ContainsAny(cmd, " \t") {
		cmd = `"` + cmd + `"`
	}
	return map[string]string{"orb": orb, "orbcmd": cmd}
}

// Fragment is the fragment for an orb at path, as indented JSON. {{orb}} and {{orbcmd}} are
// filled inside the decoded string values, so any path stays valid JSON; {{host}} is left
// for the build. Keys keep the source order (the build follows it).
func Fragment(orb string) ([]byte, error) {
	v := Vars(orb)
	r := strings.NewReplacer("{{orbcmd}}", v["orbcmd"], "{{orb}}", v["orb"])
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

// Build builds the OpenOrbit plugin from a harness source into out (a marketplace folder)
// with this fragment for an orb at path: openorbit-harness build <source> --out <out>
// --fragment <temporary file>. The {{orb}}·{{orbcmd}} variables are passed too, as the Go
// build filled them everywhere, for a source that still uses them in its texts.
func Build(r openorbit.Runner, src, out, orb string) error {
	b, err := Fragment(orb)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp("", "orbit-fragment-*.json")
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
	v := Vars(orb)
	_, err = r.Output(openorbit.Harness, "build", src, "--out", out, "--fragment", f.Name(), "--var", "orb="+v["orb"], "--var", "orbcmd="+v["orbcmd"])
	return err
}
