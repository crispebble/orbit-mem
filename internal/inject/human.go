package inject

import (
	"regexp"
	"strings"
	"unicode"
)

// The host runs its prompt hook for more than what a person types: background
// sub-agent reports, messages from other sessions, scheduled-task events and
// system notices arrive as prompts too. Recall for the request must not run on
// those. All the shapes that mark such a message live in this file.
//
// Keeping the host's message shapes here, in the domain, is where issue ORBIT-26
// put them: when the host adds a shape, this one file is all that changes.

// hostHeaders are plain-text openers with no closing mark. They are peeled off;
// what follows must be a host tag block, or the whole message is the host's own.
var hostHeaders = []string{
	"Another Claude session sent a message:",
	"[Subagent hand-back]",
}

// hostTags are tag names whose blocks are the host's own. A block opens with
// "<name" followed by a space or ">" and ends at the first "</name>".
var hostTags = []string{
	"agent-message",
	"cross-session-message",
	"teammate-message",
	"task-notification",
	"side-sessions-event",
	"ci-monitor-event",
	"scheduled-task",
}

// hostNoticePrefixes open the paragraph the host adds after a forwarded message
// block: "This came from another Claude session - ..." or, in the other variant,
// `That "other Claude session" is an agent ...`. It is the host's own text and is
// removed together with the block it follows. The paragraph runs to the next
// blank line or the end of the text; a notice joined to a question by a single
// newline is not told apart (no such case in one PC's records).
var hostNoticePrefixes = []string{
	"This came from another Claude session",
	`That "other Claude session"`,
}

// hasNoticePrefix reports whether s opens with a host notice paragraph.
func hasNoticePrefix(s string) bool {
	for _, p := range hostNoticePrefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// reminderBlock is a system notice the host appends to a prompt.
var reminderBlock = regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>`)

// trivialWords are whole-message acknowledgements and greetings (compared
// after trimming and lower-casing). Hangul-jamo-only text and short numbers
// are trivial too; see isTrivial.
var trivialWords = map[string]bool{
	"네": true, "넵": true, "응": true, "좋아": true,
	"안녕": true, "안녕하세요": true, "고마워": true, "감사합니다": true,
	"ok": true, "okay": true, "yes": true, "y": true,
	"thanks": true, "thx": true, "hi": true, "hello": true,
}

// maxTrivialDigits is the longest all-digit reply counted as trivial.
const maxTrivialDigits = 3

// PersonWords returns the part of a hook prompt a person wrote and whether the
// prompt is a person's request at all. A prompt that is empty (for example
// only an image) is a person's. System notices are removed first; what is left
// is host-made when it starts with a host shape that has nothing after it. A
// pasted host message followed by a question is a person's, and the words are
// what follows the pasted blocks (a header line and the host's notice paragraph
// after a block are part of the paste). A header followed by plain text is the
// host's own. A host shape that is not at the very start
// (text before it, or inside pasted content) does not make the prompt the host's.
// A host tag that is never closed is a person's cut paste, and the words are the
// rest from that tag on: the host always closes its blocks (some 2,500 prompts of
// one PC's records, none without the closing tag).
func PersonWords(prompt string) (words string, ok bool) {
	if strings.TrimSpace(prompt) == "" {
		return "", true
	}
	rest := strings.TrimSpace(reminderBlock.ReplaceAllString(prompt, ""))
	afterBlock := false
	for {
		if rest == "" {
			return "", false
		}
		for _, h := range hostHeaders {
			if strings.HasPrefix(rest, h) {
				rest = strings.TrimSpace(rest[len(h):])
				if openingTag(rest) == "" { // header with no block behind it: all the host's
					return "", false
				}
				break
			}
		}
		if afterBlock && hasNoticePrefix(rest) {
			if i := strings.Index(rest, "\n\n"); i >= 0 {
				rest = strings.TrimSpace(rest[i:])
			} else {
				rest = ""
			}
			afterBlock = false
			continue
		}
		name := openingTag(rest)
		if name == "" {
			return rest, true
		}
		end := strings.Index(rest, "</"+name+">")
		if end < 0 { // 호스트는 늘 닫는다: 닫히지 않은 틀은 사람이 잘라 붙인 글이다
			return rest, true
		}
		rest = strings.TrimSpace(reminderBlock.ReplaceAllString(rest[end+len("</"+name+">"):], ""))
		afterBlock = true
	}
}

// openingTag returns the host tag name s starts with, or "".
func openingTag(s string) string {
	for _, name := range hostTags {
		p := "<" + name
		if !strings.HasPrefix(s, p) || len(s) == len(p) {
			continue
		}
		if c := s[len(p)]; c == '>' || unicode.IsSpace(rune(c)) {
			return name
		}
	}
	return ""
}

// RequestText returns the text recall should use for a person's words and
// whether the request is trivial (nothing worth recalling for). A slash
// command without arguments is trivial; with arguments the arguments are the
// request. A first token with another "/" is a path, not a command. Empty
// words (an image-only request) are not trivial.
func RequestText(words string) (text string, trivial bool) {
	text = strings.TrimSpace(words)
	if strings.HasPrefix(text, "/") {
		first, args := text, ""
		if i := strings.IndexFunc(text, unicode.IsSpace); i >= 0 {
			first, args = text[:i], text[i:]
		}
		if !strings.Contains(first[1:], "/") {
			text = strings.TrimSpace(args)
			if text == "" {
				return "", true
			}
		}
	}
	return text, isTrivial(text)
}

// isTrivial reports whether text, once trimmed of surrounding space,
// punctuation and symbols (emoji included) and lower-cased, is wholly in the
// closed trivial set.
func isTrivial(text string) bool {
	if text == "" {
		return false
	}
	core := strings.ToLower(strings.TrimFunc(text, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r)
	}))
	if core == "" || trivialWords[core] {
		return true
	}
	jamo, digits := true, true
	n := 0
	for _, r := range core {
		n++
		if r < 0x3131 || r > 0x318E {
			jamo = false
		}
		if !unicode.IsDigit(r) {
			digits = false
		}
	}
	return jamo || (digits && n <= maxTrivialDigits)
}
