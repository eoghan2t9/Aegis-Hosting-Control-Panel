# AGENTS.md — AI agent entry point for Aegis

A persistent **knowledge graph of this repository** is maintained in
`graphify-out/`. It maps every Go/JS function, type, service, store method,
API handler, and doc concept, clustered into 59 labeled communities
(frontend SPA, auth, DNS, SSL, mail, backups, webapps, quota, …).
Query it **before** answering architecture questions — it is faster and more
complete than grepping.

## Query the graph (preferred)

```bash
graphify query "How does SSL certificate issuance work?"   # BFS, broad context
graphify query "Trace a domain create from API to disk" --dfs   # trace one path
graphify path "Manager" "Store" --undirected       # shortest path between concepts
graphify explain "svc_files_resolve"                       # plain-language node explanation
```

If the `graphify` CLI is unavailable, fall back to reading
`graphify-out/graph.json` directly (NetworkX node-link format:
`nodes[]`, `links[]`, `hyperedges[]`, with `community_name` on each node).

## MCP server (for MCP-capable agents)

A shared instance is **already running** on this machine as a systemd service:

```
http://127.0.0.1:8765/mcp        (graphify-mcp.service, restarts on failure)
```

Tools exposed: `query_graph`, `get_node`, `get_neighbors`, `get_community`,
`god_nodes`, `graph_stats`, `shortest_path`, `list_prs`, `get_pr_impact`,
`triage_prs`.

Manage it: `systemctl status/restart/stop graphify-mcp`

Local stdio transport (per-agent, no server needed):

```bash
graphify-mcp                                    # stdio (default)
claude mcp add aegis-graph -- graphify-mcp      # Claude Code registration
```

Note: the HTTP transport needs `uvicorn`, `fastapi` and `mcp` in the tool env
(`uv tool install --with uvicorn --with fastapi --with mcp graphifyy`).

## Raw artifacts

| File | Contents |
|---|---|
| `graphify-out/graph.json` | Full graph data (1183 nodes, 3968 edges, 59 communities) |
| `graphify-out/GRAPH_REPORT.md` | Audit report: god nodes, cohesion scores, suggested questions |
| `graphify-out/graph.html` | Interactive visualization (open in browser) |

## Keeping the graph fresh

A **post-commit hook is installed** — every `git commit` re-extracts changed
code files and rebuilds the graph automatically (no LLM needed; it requires
`graphify` on PATH, provided by `~/.local/bin/env` in this machine's shell
profiles). Skip it for one commit with `GRAPHIFY_SKIP_HOOK=1 git commit`.

Two hook caveats:

- The hook's code-only rebuild regenerates community labels as hash IDs,
  dropping the curated human-readable names in the committed graph. If a
  commit replaces curated labels, restore with
  `git checkout -- graphify-out/`.
- Doc changes are ignored by the hook. Doc concepts live in
  `graphify-out/.graphify_semantic.json`; update that file and rebuild to
  reflect them (or set `GEMINI_API_KEY` for automatic semantic extraction).

For code-only refreshes without a commit:

```bash
graphify update .      # re-extract code files, rebuild graph (no LLM)
```

## Known docs-vs-code drift (as of graph build)

The graph surfaced that `docs/ROADMAP.md` lags the code: **mail (SMTP/IMAP,
mailboxes, webmail), cron jobs, fail2ban/security centre, web-app installers,
quota enforcement, backup scheduling and remote targets (S3/SFTP) are all
already implemented** in `internal/` despite being listed as roadmap items.
Genuinely still missing: TOTP/2FA, DNSSEC, per-site PHP settings UI, reverse
proxy/Node apps, white-label theming. Verify against current source before
acting on either list.

## Honesty notes

- Edges carry `confidence` (EXTRACTED / INFERRED / AMBIGUOUS) and
  `confidence_score` (0.1–1.0). Cite `source_location` when quoting a fact.
- A health diagnostic found ~539 dangling-endpoint edges (tree-sitter
  references to implicit Go type nodes) and 37 collapsed duplicate edges.
  The graph is usable but not exhaustive — verify critical call chains in source.
- The graph is a map, not the territory: when it disagrees with the code,
  the code wins. See `docs/AI_INSTRUCTIONS.md` for repo conventions.
