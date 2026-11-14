# Requirements

A requirements model for software-defined vehicles, kept as files in git and
checked and queried by Go code that uses only the standard library. The
vocabulary and the design decisions are in
[docs/requirements-model.md](docs/requirements-model.md).

## Model files

A model is a directory with one JSON file per element, in one directory per
kind, named after the element's ID:

```
schemes/            concept schemes: allowed values for status, form, ASIL, ...
stakeholders/  ideas/  needs/  goals/  solution-concepts/
requirements/  assumptions/  hazards/  threat-scenarios/  standard-clauses/
specifications/  design-elements/  satisfactions/  verifications/
```

IDs match `[A-Za-z][A-Za-z0-9-]*` and never change. References between
elements are IDs. Unknown JSON fields are load errors, so typos don't silently
drop data. [example/ota](example/ota) is a small model of over-the-air
software updates with one hazard, one threat scenario and one UNECE R156 clause.

## Command line

```
go run ./cmd/req -m example/ota check              # structural errors, then completeness gaps
go run ./cmd/req -m example/ota trace REQ-001      # up to goals, needs, ideas; down to design and evidence
go run ./cmd/req -m example/ota impact AS-001      # what depends on an assumption
go run ./cmd/req -m example/ota impact CL-R156-7-2 # what a new edition of a clause touches
go run ./cmd/req -m example/ota evidence REQ-003   # satisfied? verified? and why
go run ./cmd/req -m example/ota unverified SPEC-001
go run ./cmd/req -m example/ota coverage CL-R156-7-2   # compliance check for a clause
go run ./cmd/req -m example/ota gate SPEC-001      # release gate: exit status 1 unless all verified
go run ./cmd/req -m example/ota find parking brake
```

Add `-json` for machine-readable output.

## Export

```
go run ./cmd/req -m example/ota -base https://your.org/req/ -o model.ttl export ttl
go run ./cmd/req -m example/ota -package 'OTA update' -o model.sysml export sysml
```

The Turtle export uses the `req:` vocabulary with OSLC RM, W3C PROV, SKOS
and Dublin Core terms, and adds the verdicts the tool computes
(`req:isSatisfied`, `req:isVerified`, `req:isCovered`; turn off with
`-computed=false`). Set `-base` to an IRI prefix you own; element IRIs are
the prefix plus the element ID. The SysML v2 export is for reading and
analysis in SysML v2 tools and is export-only. Both refuse a model with
structural errors. The web UI offers all exports as downloads.

### Into a graph store

```
req -m MODEL -o vocab.ttl export ontology   # OWL 2 RL: inverses, transitive trace links, coverage chain
req -m MODEL -o shapes.ttl export shapes    # SHACL rules that reject malformed writes
req -m MODEL -o gaps.ttl export gaps        # SHACL completeness checks, as warnings
req -m MODEL -o model.ttl export ttl
```

In GraphDB, use a repository with an OWL 2 RL ruleset and SHACL enabled.
Load `shapes.ttl` into the graph `http://rdf4j.org/schema/rdf4j#SHACLShapeGraph`,
then `vocab.ttl`, then `model.ttl`, each in its own import or transaction:
GraphDB applies the property chain behind `req:servesOrigin` only when the
ontology is committed before the data. Run `gaps.ttl` on demand through
`POST /rest/repositories/{id}/validate/text` rather than installing it:
GraphDB rejects transactions on warnings too. With the ontology loaded,
`?r req:servesOrigin :HAZ-001` lists what serves a hazard, the same set the
tool's coverage uses, and `?x req:tracesTo :NEED-001` follows every trace
link.

## Web UI

```
go run ./cmd/req -m example/ota serve             # http://127.0.0.1:8080
go run ./cmd/req -m MODEL -git -addr :8080 serve  # commit every save; listen on all interfaces
```

The overview shows structural errors, gaps, unverified requirements and the
coverage of every need, hazard, threat scenario and clause. Each element page
shows its evidence, a trace diagram, links in both directions and what a
change to it would affect.

Elements are edited as JSON, the same as their files, with the allowed
fields, reference kinds and concept values listed beside the editor. A save
is refused if it adds structural errors, and a delete is refused while
anything still refers to the element. With `-git`, every save, addition and
deletion is its own commit, authored by the git user of the machine running
the server. **Reload** rereads the files, e.g. after a `git pull`.

The server has no accounts and is meant for a few editors. It listens on
localhost by default; it refuses cross-site form posts, but put it behind
authentication before exposing it on a network.

## Language model assistant

A language model can query the model and propose changes, through a chat
page with a local model, or through MCP from any MCP client. The same tools
serve both:

| Tool | What it does |
| --- | --- |
| `get`, `find`, `list` | Look up elements |
| `trace`, `impact` | Follow trace links; what a change or a false assumption affects |
| `evidence`, `unverified`, `coverage`, `check` | Verdicts and gaps, computed by the model, never by the language model |
| `sparql` | Read-only SPARQL on the graph store, with the ontology's inferences (with `-sparql`) |
| `propose` | Suggest adding or changing one element |

Proposals change nothing. They are checked against the model (a proposal
that would add structural errors is refused), listed under **Proposals**
with the fields they add, change or remove, and applied only when a person
approves them. Approval records the language model, the approver and the
date in the element's provenance. Satisfaction claims and verifications
can't be proposed. Pending proposals live in `MODEL/.proposals/`; keep that
directory out of git.

### Chat with a local model

Any OpenAI-compatible server works: Ollama, llama.cpp's `llama-server`,
LM Studio or vLLM. Use a model that supports tool calling.

```
ollama pull qwen3:32b
go run ./cmd/req -m MODEL -llm-model qwen3:32b serve
go run ./cmd/req -m MODEL -llm-url http://127.0.0.1:1234/v1 -llm-model NAME serve   # LM Studio
```

Set `REQ_LLM_KEY` if the server needs an API key. Add
`-sparql http://localhost:7200/repositories/REPO` to give the model the graph.

### MCP

`serve` answers MCP (Streamable HTTP, JSON responses) at `/mcp`. For clients
that start a process, `req mcp` serves MCP over stdio and reads the model
directory on every call, so it sees edits made in the web UI. For Claude Code:

```
claude mcp add requirements -- /path/to/req -m /path/to/MODEL mcp
```

On localhost, the server refuses requests whose `Host` isn't local, against
DNS rebinding, and refuses cross-site browser posts.

## Importing from other tools

```
go run ./cmd/req -m MODEL -context product -prefix PLR- -n import reqif export.reqifz
go run ./cmd/req -m MODEL -context product import sphinx-needs build/needs.json
```

ReqIF (`.reqif` or zipped `.reqifz`) covers DOORS, Polarion, Codebeamer and
Jama. `-n` shows what would change without writing. Imported requirements
keep their tool and external ID, so importing again updates them; changed
text sets the status back to proposed and clears the approval. Only text,
name, grouping and refinement links are imported. Goals, concerns and other
trace links are added in the model.

## Requirement context

Every requirement states what it is on: `tool` (an AI-assisted engineering
tool), `product` (vehicle or cloud software built with such tools) or
`training-data`. A requirement's children and refinements must share its
context.

## Evidence rules

A requirement is *satisfied* when it has a satisfaction claim, and *verified*
when, for every claim, the latest verification of the claimed design element
passed at the claimed version. A requirement is also satisfied or verified
when any requirement refining it is, or when all its children are. Rejected
and obsolete elements don't count. Lifecycle status is stored; satisfied and
verified are always computed.
