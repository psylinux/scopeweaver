package harness

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
)

// TestLiveGatewayParallelToolCalls is the network-backed counterpart of
// TestParallelToolCallsKeepTheirOwnArguments: it drives the real gateway this
// deployment uses, which announces both calls in one chunk and then streams
// their arguments separately. Skipped unless NORMA_LIVE_KEY is set.
func TestLiveGatewayParallelToolCalls(t *testing.T) {
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
	prov, err := llm.NewProvider(llm.Config{Format: llm.Format("openai"), BaseURL: baseURL, APIKey: key, Model: model})
	if err != nil {
		t.Fatalf("provider: %v", err)
	}

	var mu sync.Mutex
	var got []string
	record := func(in json.RawMessage) {
		mu.Lock()
		got = append(got, string(in))
		mu.Unlock()
	}
	probe := func(name, field string) tool.CoreTool {
		return tool.Build(tool.Spec{
			Name:        name,
			Description: "Look up " + field + ".",
			Schema: map[string]any{
				"type":       "object",
				"required":   []any{field},
				"properties": map[string]any{field: map[string]any{"type": "string"}},
			},
			ReadOnly: func(json.RawMessage) bool { return true },
			Permissions: func(context.Context, json.RawMessage, permission.Context) permission.Decision {
				return permission.Allowed()
			},
			Run: func(_ context.Context, in json.RawMessage, _ *tool.ToolContext) (tool.Result, error) {
				record(in)
				return tool.Text("ok"), nil
			},
		})
	}

	_, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	in := QueryInput{
		System: []string{"You are a careful assistant. Use the tools you are given."},
		Messages: []llm.Message{llm.UserText(
			"Call BOTH tools in this single turn, in parallel: read_host with host example.com, and read_port with port 443.")},
		Tools:          tool.NewRegistry(probe("read_host", "host"), probe("read_port", "port")),
		PermissionMode: permission.ModeBypass,
	}
	drain(t, in, QueryDeps{CallModel: prov.Stream})

	if len(got) != 2 {
		t.Fatalf("tools ran %d times, want 2: %q", len(got), got)
	}
	for _, g := range got {
		var parsed map[string]any
		if err := json.Unmarshal([]byte(g), &parsed); err != nil {
			t.Errorf("tool input %q is not valid JSON: %v", g, err)
		}
	}
	t.Logf("both tools ran with %q", got)
}
