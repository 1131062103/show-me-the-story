package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"showmethestory/internal/ports"
)

type fakeAI struct {
	responses []ports.CompletionResult
	calls     int
}

func (f *fakeAI) Complete(context.Context, ports.CompletionRequest) (ports.CompletionResult, error) {
	return ports.CompletionResult{}, nil
}
func (f *fakeAI) Stream(_ context.Context, _ ports.CompletionRequest, _ func(string)) (ports.CompletionResult, error) {
	result := f.responses[f.calls]
	f.calls++
	return result, nil
}
func (f *fakeAI) ListModels(context.Context) ([]ports.ModelInfo, error)   { return nil, nil }
func (f *fakeAI) ModelContextWindow(context.Context, string) (int, error) { return 0, nil }
func (f *fakeAI) IsFatalError(error) bool                                 { return false }

type destructiveCommand struct{ calls int }

func (c *destructiveCommand) Name() string        { return "delete_outline" }
func (c *destructiveCommand) Description() string { return "delete" }
func (c *destructiveCommand) Parameters() string  { return `{ "confirm": true }` }
func (c *destructiveCommand) Safety() Safety      { return Safety{Destructive: true} }
func (c *destructiveCommand) Execute(context.Context, json.RawMessage) (Result, error) {
	c.calls++
	return Result{Text: "deleted"}, nil
}

type overwriteCommand struct{ calls int }

func (c *overwriteCommand) Name() string        { return "update_project_config" }
func (c *overwriteCommand) Description() string { return "update config" }
func (c *overwriteCommand) Parameters() string  { return `{ "confirm_overwrite": true }` }
func (c *overwriteCommand) Safety() Safety      { return Safety{Overwrite: true} }
func (c *overwriteCommand) Execute(context.Context, json.RawMessage) (Result, error) {
	c.calls++
	return Result{Text: "saved"}, nil
}

func TestRunBlocksDestructiveToolWithoutConfirmation(t *testing.T) {
	command := &destructiveCommand{}
	service := New(Dependencies{AI: &fakeAI{responses: []ports.CompletionResult{{Content: `<tool_call>{"name":"delete_outline","arguments":{}}</tool_call>`}, {Content: "I need confirmation."}}}, Model: "test", Commands: []Command{command}})
	response, history, err := service.Run(context.Background(), "delete it", nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if response != "I need confirmation." || command.calls != 0 {
		t.Fatalf("response=%q calls=%d", response, command.calls)
	}
	if len(history) != 3 || history[1].Role != "tool" || history[1].ToolResultKey != "agent.confirm_required" {
		t.Fatalf("history = %#v", history)
	}
}
func TestRunAllowsConfirmedDestructiveToolThroughCommandBoundary(t *testing.T) {
	command := &destructiveCommand{}
	service := New(Dependencies{AI: &fakeAI{responses: []ports.CompletionResult{{Content: `<tool_call>{"name":"delete_outline","arguments":{"confirm":true}}</tool_call>`}, {Content: "Deleted."}}}, Model: "test", Commands: []Command{command}})
	response, _, err := service.Run(context.Background(), "yes", nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if response != "Deleted." || command.calls != 1 {
		t.Fatalf("response=%q calls=%d", response, command.calls)
	}
}
func TestRunBlocksOverwriteWithoutExplicitApprovalFlag(t *testing.T) {
	command := &overwriteCommand{}
	service := New(Dependencies{AI: &fakeAI{responses: []ports.CompletionResult{{Content: `<tool_call>{"name":"update_project_config","arguments":{"title":"new"}}</tool_call>`}, {Content: "I need approval."}}}, Model: "test", Commands: []Command{command}})
	response, history, err := service.Run(context.Background(), "change title", nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if response != "I need approval." || command.calls != 0 {
		t.Fatalf("response=%q calls=%d", response, command.calls)
	}
	if len(history) != 3 || history[1].ToolResultKey != "agent.confirm_overwrite_required" {
		t.Fatalf("history = %#v", history)
	}
}

func TestSystemPromptInjectsSuppliedWritingRules(t *testing.T) {
	service := New(Dependencies{WritingRules: func() string { return "WRITING-SKILL-RULE" }})
	if prompt := service.systemPrompt("en"); !strings.Contains(prompt, "WRITING-SKILL-RULE") {
		t.Fatalf("English system prompt did not contain writing rules: %q", prompt)
	}
}

func TestSystemPromptOmitsWritingRulesWhenNoneEnabled(t *testing.T) {
	service := New(Dependencies{WritingRules: func() string { return "" }})
	if prompt := service.systemPrompt("en"); strings.Contains(prompt, "WRITING-SKILL-RULE") {
		t.Fatalf("system prompt contained absent writing rules: %q", prompt)
	}
}

func TestParseToolCallRetainsSingleFirstCall(t *testing.T) {
	call := ParseToolCall(`text <tool_call>{"name":"first","arguments":{"text":"}"}}</tool_call><tool_call>{"name":"second","arguments":{}}</tool_call>`)
	if call == nil || call.Name != "first" || string(call.Arguments) != `{"text":"}"}` {
		t.Fatalf("call = %#v", call)
	}
}
