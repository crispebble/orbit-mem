package claudecode

import "strings"

// SessionEnv is the environment variable Claude Code gives the processes of a conversation, its MCP servers
// included: the conversation's session id, the same id its hooks get as session_id. No public document names
// it; the value was checked on a running MCP server only.
const SessionEnv = "CLAUDE_CODE_SESSION_ID"

// MCPSession is the session of the conversation that started this MCP server, read from getenv
// (os.Getenv on the server), or "" when Claude Code did not give one. A caller then falls back to the
// server's run as the session (E-1). That fallback has a limit: when the server starts again within the
// same conversation, the new run counts as another session, and only the different-day condition keeps the
// two runs from confirming a candidate. What happens to the variable when a conversation is cleared or
// resumed was not checked.
func MCPSession(getenv func(string) string) string {
	return strings.TrimSpace(getenv(SessionEnv))
}
