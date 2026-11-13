# assist: language-model tools, MCP and chat

Lets a language model query the model and suggest changes, without letting
it decide verdicts or change anything on its own.

| File | What it does |
| --- | --- |
| [tools.go](tools.go) | The tools: `get`, `find`, `list`, `describe`, `trace`, `impact`, `evidence`, `unverified`, `coverage`, `check`, `propose`, and optional read-only `sparql` |
| [describe.go](describe.go) | Describes each kind's fields and allowed values, so a model doesn't guess field names |
| [proposals.go](proposals.go) | Pending proposals, stored as files in `MODEL/.proposals/` |
| [mcp.go](mcp.go) | The Model Context Protocol over HTTP (`/mcp` on the web server) or stdio (`req mcp`) |
| [chat.go](chat.go) | The tool loop against an OpenAI-compatible server (Ollama, llama.cpp, LM Studio, vLLM), and the system prompt |

## The rules the design enforces

- **Verdicts come from the model.** Satisfied, verified and covered come only
  from the `evidence`, `unverified` and `coverage` tools, which call the
  deterministic queries in [req](../req/).
- **Changes are proposals.** `propose` checks the change against a copy of
  the model and refuses one that would add structural errors. A person
  approves it in the web UI, which records the language model, the approver
  and the date in the element's provenance. Removed fields are flagged,
  since models tend to drop fields when they rewrite an element.
- **Claims and evidence stay human.** Satisfaction claims and verifications
  can't be proposed.

The same tools serve the built-in chat and any MCP client. Set-up is in the
[main README](../README.md#language-model-assistant).
