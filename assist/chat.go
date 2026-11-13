package assist

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// LLM is an OpenAI-compatible chat completions endpoint, as served by
// Ollama, llama.cpp's llama-server, LM Studio or vLLM.
type LLM struct {
	URL    string // base URL, e.g. http://127.0.0.1:11434/v1
	Model  string // e.g. qwen3:32b
	APIKey string // usually empty for a local server
	Client *http.Client
}

// Message is one chat message in the OpenAI format.
type Message struct {
	Role       string     `json:"role"` // system, user, assistant, tool
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	// IsError marks a tool result that was an error; not sent to the model.
	IsError bool `json:"-"`
}

// ToolCall is a call the model asks for.
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"` // JSON text
	} `json:"function"`
}

// SystemPrompt tells the model how to use the tools.
const SystemPrompt = `You help engineers with a requirements model for software-defined vehicles.

The model has needs, hazards, threat scenarios and standard clauses; goals that address them; requirements that operationalize goals, refine or decompose other requirements, or are derived from design choices; design elements; satisfaction claims; and verifications. Every element has an ID such as REQ-001.

Elements may record provenance: the sources they came from, the AI model that generated them, and who approved them.

Rules:
- Answer from the tools, not from memory. Look things up before answering. get shows all of an element's fields and links.
- Cite the element IDs you rely on.
- Whether a requirement is satisfied or verified, and whether a hazard, threat, need or clause is covered, comes only from the evidence, unverified and coverage tools. Report what they say, with their reasons; never conclude it yourself.
- To change the model, call describe for the element's kind, get the element when changing one, then propose the complete element using only the fields and values describe lists. A person approves every proposal; say that it is pending.
- Satisfaction claims and verifications record what people claimed and measured. You can't create or propose them, and you must not suggest that the user proposes them through you: tell the user to record them in the web UI with their evidence.
- Only give sources that the user named or that appear in the model. Never invent document names, versions or standards.
- Keep requirements verifiable: a requirement needs an acceptance criterion with a metric, an operator and a threshold.
- Be brief.`

const maxRounds = 10
const maxToolResult = 16000

// Reply continues a conversation: it sends history plus the user's message,
// runs the tool calls the model asks for, and returns the new messages
// (user, assistant tool calls, tool results, final assistant answer).
func (l *LLM) Reply(ctx context.Context, tb *Toolbox, history []Message, user string) ([]Message, error) {
	msgs := append([]Message{{Role: "system", Content: SystemPrompt}}, history...)
	added := []Message{{Role: "user", Content: user}}
	msgs = append(msgs, added[0])
	for round := 0; round < maxRounds; round++ {
		answer, err := l.complete(ctx, tb, msgs)
		if err != nil {
			return added, err
		}
		answer.Role = "assistant"
		msgs = append(msgs, answer)
		added = append(added, answer)
		if len(answer.ToolCalls) == 0 {
			return added, nil
		}
		for i, c := range answer.ToolCalls {
			if c.ID == "" {
				c.ID = fmt.Sprintf("call-%d-%d", round, i)
				answer.ToolCalls[i].ID = c.ID
			}
			text, isErr := tb.Call(ctx, c.Function.Name, json.RawMessage(c.Function.Arguments))
			if len(text) > maxToolResult {
				text = text[:maxToolResult] + "… (truncated; ask for less)"
			}
			res := Message{Role: "tool", Content: text, ToolCallID: c.ID, IsError: isErr}
			msgs = append(msgs, res)
			added = append(added, res)
		}
	}
	return added, fmt.Errorf("stopped after %d rounds of tool calls without an answer", maxRounds)
}

type chatTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

func (l *LLM) complete(ctx context.Context, tb *Toolbox, msgs []Message) (Message, error) {
	var tools []chatTool
	for _, t := range tb.Tools() {
		var ct chatTool
		ct.Type = "function"
		ct.Function.Name, ct.Function.Description, ct.Function.Parameters = t.Name, t.Description, t.Params
		tools = append(tools, ct)
	}
	body, _ := json.Marshal(map[string]any{
		"model":       l.Model,
		"messages":    msgs,
		"tools":       tools,
		"stream":      false,
		"temperature": 0.2,
	})
	r, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(l.URL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Message{}, err
	}
	r.Header.Set("Content-Type", "application/json")
	if l.APIKey != "" {
		r.Header.Set("Authorization", "Bearer "+l.APIKey)
	}
	client := l.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Minute}
	}
	resp, err := client.Do(r)
	if err != nil {
		return Message{}, fmt.Errorf("model server unreachable at %s: %v", l.URL, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != http.StatusOK {
		return Message{}, fmt.Errorf("model server: %s: %s", resp.Status, strings.TrimSpace(string(b[:min(len(b), 500)])))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content   *string    `json:"content"`
				ToolCalls []ToolCall `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return Message{}, fmt.Errorf("unexpected answer from the model server: %v", err)
	}
	if len(out.Choices) == 0 {
		return Message{}, errors.New("the model server returned no answer")
	}
	m := Message{ToolCalls: out.Choices[0].Message.ToolCalls}
	if c := out.Choices[0].Message.Content; c != nil {
		m.Content = stripThinking(*c)
	}
	for i := range m.ToolCalls {
		if m.ToolCalls[i].Type == "" {
			m.ToolCalls[i].Type = "function"
		}
	}
	return m, nil
}

// stripThinking removes a <think>…</think> block that reasoning models such
// as Qwen3 put before their answer.
func stripThinking(s string) string {
	if i := strings.Index(s, "<think>"); i >= 0 {
		if j := strings.Index(s[i:], "</think>"); j >= 0 {
			s = s[:i] + s[i+j+len("</think>"):]
		}
	}
	return strings.TrimSpace(s)
}
