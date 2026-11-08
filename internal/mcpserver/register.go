package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// addPlain registers a tool whose output type is any, so the SDK gives it no
// output schema (an output schema is optional in MCP, a host does not show it to
// the model, and it was most of tools/list). The structured content of a
// successful call is the handler's value as before.
func addPlain[In, Out any](srv *mcp.Server, t *mcp.Tool, h mcp.ToolHandlerFor[In, Out]) {
	mcp.AddTool(srv, t, plain(h))
}

// plain turns a typed handler into one returning any. For a failed call (an error
// result or an error) it returns no value, so the SDK attaches no
// structuredContent and the error text stays in content alone.
func plain[In, Out any](h mcp.ToolHandlerFor[In, Out]) mcp.ToolHandlerFor[In, any] {
	return func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		res, out, err := h(ctx, req, in)
		if err != nil || (res != nil && res.IsError) {
			return res, nil, err
		}
		return res, out, nil
	}
}
