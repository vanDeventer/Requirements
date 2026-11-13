# req: the command

One program for checking, querying, importing, exporting and serving a
model directory.

```
go build ./cmd/req
./req -m example/ota check
```

| Command | What it does |
| --- | --- |
| `check` | Structural errors, then completeness gaps |
| `get ID`, `find WORDS` | Look up elements |
| `trace ID`, `impact ID` | Trace links; what a change or a false assumption affects |
| `evidence ID`, `unverified [SPEC]`, `coverage ID` | Verdicts with their reasons |
| `gate [SPEC]` | Release gate for CI: exit status 1 on structural errors or unverified requirements |
| `import reqif\|sphinx-needs FILE` | Import from other requirements tools |
| `export ttl\|sysml\|ontology\|shapes\|gaps` | Write the model, the ontology or SHACL shapes |
| `serve` | The web UI, with MCP at `/mcp` and an optional chat with a local model |
| `mcp` | MCP over stdio, for Claude Code, Theia and other clients |

`-m` names the model directory and `-json` gives machine-readable output.
Run `req -h` for all flags. The [main README](../../README.md) has examples
for each.
