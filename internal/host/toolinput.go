package host

import (
	"bytes"
	"encoding/json"
	"strings"
)

// MaxToolInputRunes is how much of a tool call's input text ToolInputText keeps: enough for a command line or a
// path with its words, and a bound on what a recall reads before every tool call.
const MaxToolInputRunes = 2000

// toolInputField names the one input field that says what a call is about, for the tools whose other fields are
// content (an edit's old and new text, a written file) or flags: the shell command, the file of a file tool.
var toolInputField = map[string]string{
	"Bash":  "command",
	"Edit":  "file_path",
	"Read":  "file_path",
	"Write": "file_path",
}

// ToolInputText is the text of a tool call's input that a recall matches against: for Bash its command, for
// Edit, Read and Write their file_path, for any other tool every string value of the input in the order it is
// written (keys, numbers and booleans left out, nested values included), joined by a space. The text is
// trimmed and cut to MaxToolInputRunes. An empty, malformed or non-object input, or one without the named field
// (or with a non-string one), gives "". It only reads raw.
func ToolInputText(tool string, raw json.RawMessage) string {
	var text string
	if field, ok := toolInputField[tool]; ok {
		var obj map[string]json.RawMessage
		if json.Unmarshal(raw, &obj) != nil || json.Unmarshal(obj[field], &text) != nil {
			return ""
		}
	} else {
		text = strings.Join(stringValues(raw), " ")
	}
	text = strings.TrimSpace(text)
	if r := []rune(text); len(r) > MaxToolInputRunes {
		text = string(r[:MaxToolInputRunes])
	}
	return text
}

// stringValues lists the string values of a JSON object in document order, keys left out. It walks the tokens
// instead of decoding into a map: a map would lose the order, and the order is what keeps the text the same for
// the same input. Anything but one valid object gives none.
func stringValues(raw json.RawMessage) []string {
	if !json.Valid(raw) || !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) {
		return nil
	}
	var out []string
	if walkValue(json.NewDecoder(bytes.NewReader(raw)), &out) != nil {
		return nil
	}
	return out
}

// walkValue reads one value from dec and adds its strings to out.
func walkValue(dec *json.Decoder, out *[]string) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	switch v := tok.(type) {
	case string:
		*out = append(*out, v)
	case json.Delim: // '{' or '[': a closing one is read below, never here
		for dec.More() {
			if v == '{' {
				if _, err := dec.Token(); err != nil { // the key
					return err
				}
			}
			if err := walkValue(dec, out); err != nil {
				return err
			}
		}
		_, err = dec.Token()
		return err
	}
	return nil
}
