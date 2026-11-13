# web: the browser UI

A small web server, using only the Go standard library, for browsing,
tracing and editing a model. Start it with `req serve`.

| Path | What it is |
| --- | --- |
| [server.go](server.go) | Pages, editing, export downloads, git commits |
| [view.go](view.go) | The element JSON with linked IDs, and the trace diagram (SVG drawn on the server) |
| [assist.go](assist.go) | The chat page, the proposals page, and the localhost check |
| `templates/` | HTML templates, compiled into the binary |
| `static/` | The stylesheet, with light and dark themes |

## Pages

- **Overview**: structural errors, gaps, unverified requirements, and the
  coverage of every need, hazard, threat scenario and clause.
- **Lists per kind**, **search**, and an **element page** with evidence,
  trace diagram, links in both directions and what a change would affect.
- **Editing** with a form: drop-down menus for concept values and links,
  checkboxes, text boxes, a short explanation under each field and a
  suggested ID for new elements. The form and the element's JSON below it
  stay in step, and either can be edited ([static/form.js](static/form.js),
  plain JavaScript). Without JavaScript, the JSON editor works on its own. A
  save that would add structural errors is refused; a delete is refused
  while anything refers to the element.
- **Chat** with a local language model, and **Proposals** to approve or
  reject the changes it suggests.
- **MCP** at `/mcp`, for other language-model clients.

## Safety

Meant for a few editors on one machine or a trusted network; there are no
accounts. Writes take one lock. Cross-site form posts are refused, and on
localhost requests whose `Host` isn't local are refused too, against DNS
rebinding. With `-git`, every change is its own commit.
