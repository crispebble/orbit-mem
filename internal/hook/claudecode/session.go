package claudecode

import "strings"

// SessionEnv is the environment variable Claude Code gives the processes of a conversation, its MCP servers
// included: the conversation's session id, the same id its hooks get as session_id. No public document names
// it; the value was checked on a running MCP server only.
const SessionEnv = "CLAUDE_CODE_SESSION_ID"

// MCPSession is the session of the conversation that started this MCP server, read from getenv
// (os.Getenv on the server), or "" when Claude Code did not give one. A caller falls back to its own
// run: a missing or renamed variable then only makes confirmation slower, never wrong (E-1).
func MCPSession(getenv func(string) string) string {
	return strings.TrimSpace(getenv(SessionEnv))
}
