package rehydrate

import (
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/mcp"
)

// TestToolCall_UsesTheRealExpandSchema pins that a tool pointer reads as a call the model can copy:
// internal/mcp's own tool name and argument name, the id quoted as a JSON string.
func TestToolCall_UsesTheRealExpandSchema(t *testing.T) {
	f, ok := reflect.TypeOf(mcp.ExpandArgs{}).FieldByName("ToolUseID")
	require.True(t, ok)
	arg := strings.Split(f.Tag.Get("json"), ",")[0]

	require.Equal(t, mcp.ToolExpand+"("+arg+`="toolu_1")`, toolCall("toolu_1"))
	require.Equal(t, `expand(tool_use_id="a\"b")`, toolCall(core.ToolUseID(`a"b`)),
		"an id that would break the call is escaped, never pasted raw")

	cp := ckEmpty()
	cp.Pointers.Tools = []checkpoint.ToolPointer{{ToolUseID: "toolu_9", Hash: hashOf("x"), Summary: "go run ./cmd/seed"}}
	got := buildPointers(bg(), requestFor(t, cp, generousTestBudget), depsWith(&spyLogger{}), nil)
	require.Equal(t, `- expand(tool_use_id="toolu_9") `+hashOf("x").String()+" — go run ./cmd/seed\n", got.units[0].text)
}
