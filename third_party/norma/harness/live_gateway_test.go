package harness

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
)

// TestLiveGatewayDeliversToolArguments is a manual, network-backed check of the
// streaming loop against the real Chat Completions gateway this deployment
// uses. It is skipped unless NORMA_LIVE_KEY is set.
//
// It exists because the empty-argument bug only reproduced with a gateway that
// splits a tool_call across chunks with content deltas in between; a scripted
// model proves the loop, this proves the loop plus the wire.
func TestLiveGatewayDeliversToolArguments(t *testing.T) {
	key := os.Getenv("NORMA_LIVE_KEY")
	if key == "" {
		t.Skip("NORMA_LIVE_KEY unset")
	}
	baseURL := os.Getenv("NORMA_LIVE_BASE_URL")
	if baseURL == "" {
		baseURL = "https://code.verboo.ai/router/v1"
	}
	model := os.Getenv("NORMA_LIVE_MODEL")
	if model == "" {
		model = "deepseek-v4-pro-0813"
	}

	prov, err := llm.NewProvider(llm.Config{
		Format:  llm.FormatOpenAI,
		BaseURL: baseURL,
		APIKey:  key,
		Model:   model,
	})
	if err != nil {
		t.Fatalf("provider: %v", err)
	}

	var got []string
	probe := tool.Build(tool.Spec{
		Name:        "list_dir",
		Description: "List the entries of a directory.",
		Schema: map[string]any{
			"type":     "object",
			"required": []any{"path"},
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "description": "Absolute directory path."},
			},
		},
		ReadOnly: func(json.RawMessage) bool { return true },
		Permissions: func(context.Context, json.RawMessage, permission.Context) permission.Decision {
			return permission.Allowed()
		},
		Run: func(_ context.Context, in json.RawMessage, _ *tool.ToolContext) (tool.Result, error) {
			got = append(got, string(in))
			return tool.Text("bin\netc\nhome"), nil
		},
	})

	_, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	in := QueryInput{
		System:         []string{"You are a careful assistant. Use the tools you are given."},
		Messages:       []llm.Message{llm.UserText("List the directory /etc using the list_dir tool, then say what you saw in one sentence.")},
		Tools:          tool.NewRegistry(probe),
		PermissionMode: permission.ModeBypass,
	}
	term := drain(t, in, QueryDeps{CallModel: prov.Stream})

	if len(got) == 0 {
		t.Fatal("the tool never ran")
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(got[0]), &parsed); err != nil {
		t.Fatalf("tool input %q is not JSON: %v", got[0], err)
	}
	if _, ok := parsed["path"]; !ok {
		t.Fatalf("tool ran with %s, want a path argument", got[0])
	}
	t.Logf("tool ran with %s; conversation has %d messages", got[0], len(term.Messages))
}
