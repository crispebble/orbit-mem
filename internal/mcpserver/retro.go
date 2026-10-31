package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onidot-labs/openorbit/internal/config"
	"github.com/onidot-labs/openorbit/internal/memory"
	"github.com/onidot-labs/openorbit/internal/retro"
)

// NewRetro is the server a headless retrospective talks to: it reads memories,
// reads the material page by page and submits the result once.
func NewRetro(s *memory.Store, paths config.Paths, id, version string) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "orbit-retro", Version: version}, nil)
	h := handlers{s: s, o: Options{Version: version}}
	mcp.AddTool(srv, &mcp.Tool{Name: "memory_find", Description: "이미 있는 기억을 찾는다(모든 범위). 중복 확인용.", Annotations: readHints()}, func(ctx context.Context, r *mcp.CallToolRequest, in FindIn) (*mcp.CallToolResult, FindOut, error) {
		if in.Scope == "" {
			in.Scope = "all"
		}
		return h.find(ctx, r, in)
	})
	mcp.AddTool(srv, &mcp.Tool{Name: "memory_get", Description: "id로 기억을 읽는다.", Annotations: readHints()}, h.get)
	mcp.AddTool(srv, &mcp.Tool{Name: "retro_material", Description: "회고 재료의 한 쪽을 읽는다. page 1부터 마지막 쪽까지 모두 읽는다.", Annotations: readHints()},
		func(_ context.Context, _ *mcp.CallToolRequest, in PageIn) (*mcp.CallToolResult, any, error) {
			page := in.Page
			if page == 0 {
				page = 1
			}
			text, _, err := retro.ReadPage(s, paths, id, page)
			if err != nil {
				r, _ := toolError(err)
				return r, nil, nil
			}
			return textResult(text), nil, nil
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "retro_read_file", Description: "반영 대상 파일(apply_file 제안을 만들 때)이나 판정 대상 근거 파일의 현재 내용을 읽는다.", Annotations: readHints()},
		func(_ context.Context, _ *mcp.CallToolRequest, in FileIn) (*mcp.CallToolResult, any, error) {
			text, err := retro.ReadTarget(paths, id, in.Path, in.Page)
			if err != nil {
				r, _ := toolError(err)
				return r, nil, nil
			}
			return textResult(text), nil, nil
		})
	// retro_submit only adds memories and proposals, and takes one submission.
	mcp.AddTool(srv, &mcp.Tool{Name: "retro_submit", Description: "회고 결과(일지·기억·짚을 점·제안)를 한 번 제출한다.", Annotations: writeHints(false, true)},
		func(_ context.Context, _ *mcp.CallToolRequest, in retro.Submission) (*mcp.CallToolResult, any, error) {
			report, err := retro.Submit(s, paths, id, in)
			if err != nil {
				r, _ := toolError(fmt.Errorf("제출 실패: %w", err))
				return r, nil, nil
			}
			return textResult(report), nil, nil
		})
	return srv
}

// PageIn is retro_material's input.
type PageIn struct {
	Page int `json:"page,omitempty" jsonschema:"읽을 쪽(1부터)"`
}

// FileIn is retro_read_file's input.
type FileIn struct {
	Path string `json:"path" jsonschema:"반영 대상이나 판정 대상 파일 절대경로"`
	Page int    `json:"page,omitempty" jsonschema:"판정 대상 파일의 쪽(1부터). 여러 쪽이면 모두 읽는다"`
}

func textResult(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}
