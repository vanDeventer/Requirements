package assist

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"sync"
)

// The Model Context Protocol, as JSON-RPC 2.0 over Streamable HTTP (one
// JSON response per POST) or over stdio (one message per line). Only tools
// are offered; that is all a client needs to query and propose.

var protocolVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

const instructions = `This server is a requirements model for software-defined vehicles. ` +
	`Answer from the tools and cite element IDs. Satisfied, verified and covered come only from the evidence, unverified and coverage tools; never infer them yourself. ` +
	`To change the model, use propose: a person approves each change in the web UI.`

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// handle answers one message. It returns nil for notifications and responses,
// which get no reply.
func (tb *Toolbox) handle(ctx context.Context, raw []byte) *rpcResponse {
	var r rpcRequest
	if err := json.Unmarshal(raw, &r); err != nil {
		return &rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32700, "parse error"}}
	}
	if r.Method == "" { // a response from the client; we never send requests
		return nil
	}
	if len(r.ID) == 0 { // notification
		return nil
	}
	res := &rpcResponse{JSONRPC: "2.0", ID: r.ID}
	if r.JSONRPC != "2.0" {
		res.Error = &rpcError{-32600, "jsonrpc must be 2.0"}
		return res
	}
	switch r.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(r.Params, &p)
		v := protocolVersions[0]
		if slices.Contains(protocolVersions, p.ProtocolVersion) {
			v = p.ProtocolVersion
		}
		res.Result = map[string]any{
			"protocolVersion": v,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "requirements", "version": "0.1"},
			"instructions":    instructions,
		}
	case "ping":
		res.Result = map[string]any{}
	case "tools/list":
		var ts []map[string]any
		for _, t := range tb.tools {
			ts = append(ts, map[string]any{"name": t.Name, "description": t.Description, "inputSchema": t.Params})
		}
		res.Result = map[string]any{"tools": ts}
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(r.Params, &p); err != nil || p.Name == "" {
			res.Error = &rpcError{-32602, "params must have a tool name"}
			return res
		}
		text, isErr := tb.Call(ctx, p.Name, p.Arguments)
		res.Result = map[string]any{
			"content": []map[string]any{{"type": "text", "text": text}},
			"isError": isErr,
		}
	default:
		res.Error = &rpcError{-32601, "method not found: " + r.Method}
	}
	return res
}

// ServeHTTP implements the Streamable HTTP transport without server-sent
// events: each POST carries one message and gets one JSON response, or 202
// for a notification. The caller is responsible for origin checks.
func (tb *Toolbox) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "this server doesn't stream; POST JSON-RPC messages", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	res := tb.handle(r.Context(), body)
	if res == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

// ServeStdio runs the stdio transport until in is closed: one JSON-RPC
// message per line in, one response per line out.
func (tb *Toolbox) ServeStdio(ctx context.Context, in io.Reader, out io.Writer) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64<<10), 8<<20)
	var mu sync.Mutex
	enc := json.NewEncoder(out)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		if res := tb.handle(ctx, line); res != nil {
			mu.Lock()
			err := enc.Encode(res)
			mu.Unlock()
			if err != nil {
				return err
			}
		}
	}
	return sc.Err()
}
