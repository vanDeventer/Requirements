# Requirements

A requirements model for software-defined vehicles, kept as files in git and
checked and queried by Go code that uses only the standard library. The
vocabulary and the design decisions are in
[Requirements Vocabulary Semantics and Relationships.md](Requirements%20Vocabulary%20Semantics%20and%20Relationships.md).

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
