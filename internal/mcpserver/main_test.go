package mcpserver

import (
	"os"
	"testing"

	"github.com/onidot-labs/orbit-mem/internal/i18n"
)

// orb mcp는 언어 설정과 무관하게 한국어로 답한다(cmd/orb cmdMCP). 시험도 같은 언어로 돈다.
func TestMain(m *testing.M) {
	i18n.Set(i18n.KO)
	os.Exit(m.Run())
}
