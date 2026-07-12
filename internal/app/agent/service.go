// Package agent runs the project assistant without giving it direct persistence access.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"showmethestory/internal/app/chat"
	"showmethestory/internal/app/runtime"
	"showmethestory/internal/domain/project"
	"showmethestory/internal/ports"
)

const defaultMaxSteps = 8

var (
	ErrTaskRunning   = errors.New("another task is already running")
	ErrNoAIClient    = errors.New("AI client is required")
	ErrModelRequired = errors.New("AI model is required")
)

// Command is the sole mutation boundary available to assistant tools. Implementations
// must delegate to v2 application services or ProjectSession methods; they must never
// mutate files or a project snapshot directly.
type Command interface {
	Name() string
	Description() string
	Parameters() string
	Execute(context.Context, json.RawMessage) (Result, error)
}

// Safety describes a command whose confirmation requirements are enforced before it
// reaches the command implementation.
type Safety struct {
	Destructive bool
	Overwrite   bool
}

type SafeCommand interface {
	Command
	Safety() Safety
}

type Result struct {
	Text string
	Key  string
	Args []string
}

type Step struct {
	Role           string
	Content        string
	ToolCall       *chat.ToolCall
	ToolResult     string
	ToolResultKey  string
	ToolResultArgs []string
}

type Dependencies struct {
	Session      *runtime.ProjectSession
	Tasks        *runtime.TaskManager
	AI           ports.AIClient
	Events       ports.EventPublisher
	Model        string
	MaxTokens    int
	Commands     []Command
	WritingRules func() string
}

type Service struct {
	session      *runtime.ProjectSession
	tasks        *runtime.TaskManager
	ai           ports.AIClient
	events       ports.EventPublisher
	model        string
	maxTokens    int
	commands     map[string]Command
	writingRules func() string
}

func New(deps Dependencies) *Service {
	commands := make(map[string]Command, len(deps.Commands))
	for _, command := range deps.Commands {
		if command != nil {
			commands[command.Name()] = command
		}
	}
	return &Service{session: deps.Session, tasks: deps.Tasks, ai: deps.AI, events: deps.Events, model: deps.Model, maxTokens: deps.MaxTokens, commands: commands, writingRules: deps.WritingRules}
}

// Start executes an assistant turn as exclusive work. Command implementations that
// launch background work must register it as a child of this task before returning.
func (s *Service) Start(userMessage string, history []Step, maxSteps int, done func(string, []Step, error)) error {
	if s.tasks == nil {
		return errors.New("task manager is required")
	}
	task, ok := s.tasks.Start("agent")
	if !ok {
		return ErrTaskRunning
	}
	go func() {
		result, steps, err := s.Run(task.Context(), userMessage, history, maxSteps)
		task.Done(err == nil)
		if done != nil {
			done(result, steps, err)
		}
	}()
	return nil
}

func (s *Service) Run(ctx context.Context, userMessage string, history []Step, maxSteps int) (string, []Step, error) {
	if s.ai == nil {
		return "", history, ErrNoAIClient
	}
	if strings.TrimSpace(s.model) == "" {
		return "", history, ErrModelRequired
	}
	if maxSteps <= 0 {
		maxSteps = defaultMaxSteps
	}
	language := s.language()
	messages := s.messages(history, userMessage, language)
	for step := 0; step < maxSteps; step++ {
		if err := ctx.Err(); err != nil {
			return "", history, errors.New(localized(language, "task_cancelled"))
		}
		result, err := s.ai.Stream(ctx, ports.CompletionRequest{Model: s.model, Messages: messages, MaxTokens: s.effectiveMaxTokens()}, nil)
		if err != nil {
			return "", history, fmt.Errorf("assistant API: %w", err)
		}
		call := ParseToolCall(result.Content)
		if outputTruncated(result.FinishReason, result.Content, call) {
			return "", history, errors.New(localized(language, "output_truncated"))
		}
		if call == nil {
			content := StripToolCallTags(result.Content)
			history = append(history, Step{Role: "assistant", Content: content})
			return content, history, nil
		}
		history = append(history, Step{Role: "assistant", ToolCall: call})
		toolResult, err := s.execute(ctx, call, language)
		if err != nil {
			toolResult = Result{Text: fmt.Sprintf("%s: %v", localized(language, "tool_error"), err), Key: "agent.tool_exec_error", Args: []string{err.Error()}}
		}
		history = append(history, Step{Role: "tool", ToolResult: toolResult.Text, ToolResultKey: toolResult.Key, ToolResultArgs: toolResult.Args})
		if s.events != nil {
			s.events.Publish("agent_tool_result", map[string]any{"name": call.Name, "result": toolResult.Text, "key": toolResult.Key, "args": toolResult.Args})
		}
		encoded, _ := json.Marshal(call)
		messages = append(messages, ports.Message{Role: "assistant", Content: "<tool_call>\n" + string(encoded) + "\n</tool_call>"}, ports.Message{Role: "user", Content: toolResultLabel(language) + "\n" + toolResult.Text})
	}
	return localized(language, "max_steps"), history, nil
}

func (s *Service) execute(ctx context.Context, call *chat.ToolCall, language string) (Result, error) {
	command := s.commands[call.Name]
	if command == nil {
		return Result{Text: fmt.Sprintf(localized(language, "unknown_tool"), call.Name), Key: "agent.unknown_tool", Args: []string{call.Name}}, nil
	}
	if guarded, ok := command.(SafeCommand); ok {
		safety := guarded.Safety()
		if safety.Destructive && !confirmed(call.Arguments, "confirm") {
			return Result{Text: fmt.Sprintf(localized(language, "confirm_required"), call.Name), Key: "agent.confirm_required", Args: []string{call.Name}}, nil
		}
		if safety.Overwrite && !confirmed(call.Arguments, "confirm_overwrite") {
			return Result{Text: fmt.Sprintf(localized(language, "overwrite_required"), call.Name), Key: "agent.confirm_overwrite_required", Args: []string{call.Name}}, nil
		}
	}
	return command.Execute(ctx, call.Arguments)
}

func (s *Service) messages(history []Step, current, language string) []ports.Message {
	messages := []ports.Message{{Role: "system", Content: s.systemPrompt(language)}}
	lastUser := -1
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == "user" {
			lastUser = i
			break
		}
	}
	for i, item := range history {
		switch item.Role {
		case "user":
			if i != lastUser {
				messages = append(messages, ports.Message{Role: "user", Content: item.Content})
			}
		case "assistant":
			if item.ToolCall != nil {
				encoded, _ := json.Marshal(item.ToolCall)
				messages = append(messages, ports.Message{Role: "assistant", Content: "<tool_call>\n" + string(encoded) + "\n</tool_call>"})
			} else {
				messages = append(messages, ports.Message{Role: "assistant", Content: item.Content})
			}
		case "tool":
			messages = append(messages, ports.Message{Role: "user", Content: toolResultLabel(language) + "\n" + item.ToolResult})
		}
	}
	return append(messages, ports.Message{Role: "user", Content: current})
}

func (s *Service) language() string {
	if s.session == nil {
		return project.LangZH
	}
	snapshot := s.session.Snapshot()
	if snapshot == nil || snapshot.Project == nil || snapshot.Project.Config == nil {
		return project.LangZH
	}
	return project.NormalizeLanguage(snapshot.Project.Config.Language)
}
func (s *Service) effectiveMaxTokens() int {
	if s.maxTokens < 8192 {
		return 8192
	}
	return s.maxTokens
}
func toolResultLabel(language string) string {
	if language == project.LangEN {
		return "[Tool result]"
	}
	return "[工具结果]"
}

func (s *Service) systemPrompt(language string) string {
	var tools strings.Builder
	for _, command := range s.commands {
		fmt.Fprintf(&tools, "- %s: %s\n  parameters: %s\n", command.Name(), command.Description(), command.Parameters())
	}
	rules := ""
	if s.writingRules != nil {
		rules = strings.TrimSpace(s.writingRules())
	}
	if language == project.LangEN {
		prompt := "You are a novel-writing assistant. Use a tool only with <tool_call>{\"name\":\"tool_name\",\"arguments\":{}}</tool_call>. Call exactly one tool at a time and wait for its result. Do not emit explanatory text beside a tool call. Edit is not delete: never call destructive tools to edit content. Destructive commands require an explicit user-confirmed range and confirm=true. Overwriting populated configuration requires an explicit user approval and confirm_overwrite=true."
		if rules != "" {
			prompt += "\n\n" + rules
		}
		return prompt + "\n\nAvailable tools:\n" + tools.String()
	}
	prompt := "你是小说创作助手。调用工具时仅使用 <tool_call>{\"name\":\"工具名\",\"arguments\":{}}</tool_call>，一次只能调用一个工具并等待结果。工具调用旁不要输出解释文字。修改不等于删除：不得用删除工具实现修改。危险操作必须先获得用户对具体范围的明确确认，再传 confirm=true；覆盖已有配置必须先获用户确认，再传 confirm_overwrite=true。"
	if rules != "" {
		prompt += "\n\n" + rules
	}
	return prompt + "\n\n可用工具：\n" + tools.String()
}

func confirmed(raw json.RawMessage, field string) bool {
	var values map[string]bool
	return json.Unmarshal(raw, &values) == nil && values[field]
}
func localized(language, key string) string {
	en := language == project.LangEN
	switch key {
	case "task_cancelled":
		if en {
			return "Assistant task was cancelled"
		}
		return "助手任务已取消"
	case "output_truncated":
		if en {
			return "Assistant output was truncated while producing a tool call"
		}
		return "助手输出在生成工具调用时被截断"
	case "tool_error":
		if en {
			return "Tool execution failed"
		}
		return "工具执行失败"
	case "max_steps":
		if en {
			return "Maximum assistant steps reached"
		}
		return "已达到助手最大执行步数"
	case "unknown_tool":
		if en {
			return "Unknown tool: %s"
		}
		return "未知工具：%s"
	case "confirm_required":
		if en {
			return "Confirmation is required before %s. Restate the exact affected range to the user and wait for an explicit confirmation, then use confirm=true."
		}
		return "%s 需要确认。请先向用户复述确切影响范围并等待明确确认，再使用 confirm=true。"
	case "overwrite_required":
		if en {
			return "Explicit approval is required before overwriting existing values with %s; use confirm_overwrite=true only after approval."
		}
		return "%s 覆盖已有值前需明确同意；仅在获准后使用 confirm_overwrite=true。"
	}
	return key
}
