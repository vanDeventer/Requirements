# exchange: importing from other requirements tools

Reads requirements exported by other tools and merges them into a model.

| File | What it does |
| --- | --- |
| [reqif.go](reqif.go) | Reads ReqIF 1.2 (`.reqif`, or zipped `.reqifz`), the exchange format of DOORS, Polarion, Codebeamer and Jama |
| [needs.go](needs.go) | Reads Sphinx-Needs' `needs.json` |
| [exchange.go](exchange.go) | Merges what was read into a model |

## What is imported

Only what the tools state unambiguously: requirement text and name, the
documents that group them (as Specifications) and refinement links. Goals,
concerns and other trace links are added in this model, where they can be
checked. Headings, unknown relation types and generic links are listed in
the import notes rather than guessed at.

## Importing again

Each imported element remembers its tool and its ID there. Importing the
same source again updates those elements instead of adding new ones. When
the text changed in the tool, the status goes back to proposed and any
approval is cleared, since what was approved no longer matches.

Run it with `req import`; see the [main README](../README.md#importing-from-other-tools).
