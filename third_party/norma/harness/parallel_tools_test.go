package harness

import (
	"encoding/json"
	"testing"

	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
)

// TestParallelToolCallsKeepTheirOwnArguments guards the streaming loop against
// merging the arguments of concurrent tool calls.
//
// A Chat Completions gateway announces every parallel call in one chunk, each
// with its own index, and only then streams the arguments per index. With a
// single argument buffer the first call was dispatched with "{}" (its arguments
// had not arrived yet) and the second call received both fragments concatenated
// into invalid JSON.
func TestParallelToolCallsKeepTheirOwnArguments(t *testing.T) {
	var got []string
	m := &scriptedModel{turns: [][]llm.StreamEvent{
		{
			{Type: llm.SEToolUseStart, ToolID: "c0", ToolName: "probe", ToolIndex: 0},
			{Type: llm.SEToolUseStart, ToolID: "c1", ToolName: "probe", ToolIndex: 1},
			{Type: llm.SEToolInputJSON, ToolIndex: 0, Text: `{"command":`},
			{Type: llm.SEToolInputJSON, ToolIndex: 1, Text: `{"command":"ls"}`},
			{Type: llm.SEToolInputJSON, ToolIndex: 0, Text: `"echo hi"}`},
			{Type: llm.SEMessageDelta, StopReason: "tool_use"},
			{Type: llm.SEMessageStop},
		},
		textTurn("done"),
	}}
	in := QueryInput{
		Messages:       []llm.Message{llm.UserText("go")},
		Tools:          tool.NewRegistry(boundaryProbeTool(&got)),
		PermissionMode: permission.ModeBypass,
	}
	drain(t, in, QueryDeps{CallModel: m.call})

	if len(got) != 2 {
		t.Fatalf("tool ran %d times, want 2: %q", len(got), got)
	}
	want := []string{`{"command":"echo hi"}`, `{"command":"ls"}`}
	for _, w := range want {
		var seen int
		for _, g := range got {
			if g == w {
				seen++
			}
		}
		if seen != 1 {
			t.Errorf("input %s ran %d times, want exactly 1: %q", w, seen, got)
		}
	}
	// Every input must be independently valid JSON: a merged buffer produced
	// `{"command":"echo hi"}{"command":"ls"}`.
	for _, g := range got {
		var probe map[string]any
		if err := json.Unmarshal([]byte(g), &probe); err != nil {
			t.Errorf("tool input %q is not valid JSON: %v", g, err)
		}
	}
}
