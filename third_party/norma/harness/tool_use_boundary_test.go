package harness

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
)

// boundaryProbeTool records the raw input of every invocation, so a test can assert on
// what the tool actually ran with rather than on what the accumulated message
// claims it ran with. The two disagreeing is the bug under test.
func boundaryProbeTool(got *[]string) tool.CoreTool {
	return tool.Build(tool.Spec{
		Name: "probe",
		Schema: map[string]any{
			"type":       "object",
			"required":   []any{"command"},
			"properties": map[string]any{"command": map[string]any{"type": "string"}},
		},
		ReadOnly: func(json.RawMessage) bool { return true },
		Permissions: func(context.Context, json.RawMessage, permission.Context) permission.Decision {
			return permission.Allowed()
		},
		Run: func(_ context.Context, in json.RawMessage, _ *tool.ToolContext) (tool.Result, error) {
			*got = append(*got, string(in))
			return tool.Text("ok"), nil
		},
	})
}

// TestContentDeltasDoNotFinalizeToolBlock guards the streaming loop against a
// tool_use block being closed by a text or thinking delta.
//
// Chat Completions gateways open a tool_call on the chunk that carries id and
// name and stream function.arguments several chunks later, with content or
// reasoning deltas in between. Treating those deltas as a block boundary
// dispatched the tool with raw == "{}" and dropped every argument delta that
// followed, because curInput.WriteString is gated on `building`. The tool ran
// empty while acc.Message() recorded the real arguments, which is what a caller
// then replays to the model.
func TestContentDeltasDoNotFinalizeToolBlock(t *testing.T) {
	cases := []struct {
		name   string
		deltas []llm.StreamEvent
	}{
		{
			name: "text before and between argument deltas",
			deltas: []llm.StreamEvent{
				{Type: llm.SEToolUseStart, ToolID: "c1", ToolName: "probe"},
				{Type: llm.SETextDelta, Text: "I'll "},
				{Type: llm.SEToolInputJSON, Text: `{"command":`},
				{Type: llm.SETextDelta, Text: "check the headers."},
				{Type: llm.SEToolInputJSON, Text: `"echo hi"}`},
				{Type: llm.SEMessageDelta, StopReason: "tool_use"},
				{Type: llm.SEMessageStop},
			},
		},
		{
			name: "thinking before the arguments",
			deltas: []llm.StreamEvent{
				{Type: llm.SEToolUseStart, ToolID: "c1", ToolName: "probe"},
				{Type: llm.SEThinkingDelta, Text: "Retrieve the "},
				{Type: llm.SEThinkingDelta, Text: "headers first."},
				{Type: llm.SEToolInputJSON, Text: `{"command":"echo hi"}`},
				{Type: llm.SEMessageDelta, StopReason: "tool_use"},
				{Type: llm.SEMessageStop},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			m := &scriptedModel{turns: [][]llm.StreamEvent{tc.deltas, textTurn("done")}}
			in := QueryInput{
				Messages:       []llm.Message{llm.UserText("go")},
				Tools:          tool.NewRegistry(boundaryProbeTool(&got)),
				PermissionMode: permission.ModeBypass,
			}
			term := drain(t, in, QueryDeps{CallModel: m.call})

			if len(got) != 1 {
				t.Fatalf("tool ran %d times, want 1: %q (a content delta closed the tool_use block before the arguments arrived)", len(got), got)
			}
			if want := `{"command":"echo hi"}`; got[0] != want {
				t.Errorf("tool ran with %s, want %s", got[0], want)
			}

			// The replayed history must agree with what the tool ran with.
			var found bool
			for _, msg := range term.Messages {
				for _, b := range msg.Content {
					if b.Type == llm.BlockToolUse && b.ID == "c1" {
						found = true
						if string(b.Input) != `{"command":"echo hi"}` {
							t.Errorf("replayed tool_use input = %s, want the full arguments", b.Input)
						}
					}
				}
			}
			if !found {
				t.Error("no tool_use block for c1 in the conversation snapshot")
			}
		})
	}
}
