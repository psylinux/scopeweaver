package main

import "testing"

func TestTaskDirRe(t *testing.T) {
	cases := []struct {
		dir  string
		want string
	}{
		{"/app/data/tasks/16/i8464", "16"},
		{"/app/data/tasks/16", "16"},
		{"/app/data/tasks/203/i4130/sub", "203"},
		{"tasks/7/x", "7"},
		{"/app/data/sessions/abc123", ""},
		{"/app/data/tasks/", ""},
		{"/home/user/project", ""},
	}
	for _, c := range cases {
		got := ""
		if m := taskDirRe.FindStringSubmatch(c.dir); m != nil {
			got = m[1]
		}
		if got != c.want {
			t.Errorf("taskDirRe(%q) = %q, want %q", c.dir, got, c.want)
		}
	}
}

func TestResolveAgentFlagWins(t *testing.T) {
	t.Setenv("MAILOTP_AGENT", "envagent")
	if got := resolveAgent("flagagent"); got != "flagagent" {
		t.Fatalf("flag should win, got %q", got)
	}
}

func TestResolveAgentEnv(t *testing.T) {
	t.Setenv("MAILOTP_AGENT", "envagent")
	t.Setenv("ARTEX_TASK_ID", "99")
	if got := resolveAgent(""); got != "envagent" {
		t.Fatalf("MAILOTP_AGENT should be used, got %q", got)
	}
}
