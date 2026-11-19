package agent

import "testing"

func TestParseClaude(t *testing.T) {
	evs := parseClaude([]byte(`{"type":"system","subtype":"init","session_id":"s1"}`))
	if len(evs) != 1 || evs[0].SessionID != "s1" {
		t.Fatalf("init: %+v", evs)
	}
	evs = parseClaude([]byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"hi"},{"type":"tool_use","name":"Bash","input":{"command":"git status"}}]}}`))
	if len(evs) != 2 || evs[0].Text != "hi" || evs[1].Tool != "Bash" || evs[1].Input != "git status" {
		t.Fatalf("assistant: %+v", evs)
	}
	evs = parseClaude([]byte(`{"type":"user","message":{"content":[{"type":"tool_result","content":[{"type":"text","text":"clean"}],"is_error":false}]}}`))
	if len(evs) != 1 || evs[0].Type != EvToolResult || evs[0].Text != "clean" {
		t.Fatalf("tool_result: %+v", evs)
	}
	evs = parseClaude([]byte(`{"type":"result","subtype":"success","result":"OK","is_error":false,"total_cost_usd":0.09,"session_id":"s1"}`))
	if len(evs) != 1 || evs[0].Type != EvResult || evs[0].Text != "OK" || evs[0].CostUSD != 0.09 {
		t.Fatalf("result: %+v", evs)
	}
}

func TestParseCodex(t *testing.T) {
	ev, _, _ := parseCodex([]byte(`{"type":"thread.started","thread_id":"th1"}`))
	if ev == nil || ev.SessionID != "th1" {
		t.Fatalf("thread: %+v", ev)
	}
	ev, final, _ := parseCodex([]byte(`{"type":"item.completed","item":{"id":"i1","type":"agent_message","text":"OK"}}`))
	if ev == nil || ev.Text != "OK" || final != "OK" {
		t.Fatalf("message: %+v", ev)
	}
	ev, _, _ = parseCodex([]byte(`{"type":"item.completed","item":{"id":"i0","type":"error","message":"deprecated flag"}}`))
	if ev == nil || ev.Type != EvLog {
		t.Fatalf("error item should be a log, got %+v", ev)
	}
	ev, _, _ = parseCodex([]byte(`{"type":"item.completed","item":{"type":"command_execution","command":"ls","aggregated_output":"x","exit_code":2}}`))
	if ev == nil || ev.Type != EvToolResult || ev.Text != "exit 2\nx" {
		t.Fatalf("command: %+v", ev)
	}
	_, _, usage := parseCodex([]byte(`{"type":"turn.completed","usage":{"input_tokens":1}}`))
	if usage == nil {
		t.Fatal("usage missing")
	}
}

func TestCodexArgsResume(t *testing.T) {
	c := &Codex{}
	a := c.args(Spec{Workdir: "/w", Prompt: "p", SessionID: "th1"})
	got := a[len(a)-3:]
	if got[0] != "resume" || got[1] != "th1" || got[2] != "p" {
		t.Fatalf("resume args %v", a)
	}
}
