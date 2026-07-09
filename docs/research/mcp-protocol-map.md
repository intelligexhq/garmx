# MCP Protocol Map & GarmX Roadmap Alignment

**Living document.** Two jobs:

1. Ground GarmX in the MCP protocol as it actually stands — history, who owns
   it, how the industry is adopting it, and where it sits in the wider Linux
   Foundation open-source landscape.
2. Map GarmX **primitive-by-primitive and method-by-method** against MCP: what
   it supports, what it synthesizes, what is deferred, and what has simply
   **never been discussed** — then overlay the MCP roadmap and derive a GarmX
   roadmap from the gaps.

This is a strategy instrument, not a spec. It exists so GarmX moves *with* the
direction MCP is heading (stateless core, Tasks, registry, authorization) and
stays coherent with the other LF standards it rides — chiefly **OpenTelemetry**.
Keep it current; when a decision here hardens, promote it to
[`architecture.md`](../architecture.md) or [`discovery.md`](../discovery.md).

Cross-references: [`architecture.md`](../architecture.md) (method handling,
schema), [`discovery.md`](../discovery.md) (decided/open/dropped),
[`client-handshakes.md`](client-handshakes.md) (captured client behavior).

---

## Part 0 — Positioning & value

**Positioning (decided 2026-07-09).** GarmX is the **local-first observability
plane for MCP** — *see, audit, and trust what your AI coding agents actually do
across every tool, from a single Go binary.*

- **Adopter / buyer:** the developer-as-operator — a solo dev or small team
  running local MCP clients (Claude Code, OpenCode) today.
- **Job-to-be-done:** "I've wired MCP servers across my agents and can't see what
  they're doing. I want one place to watch every tool call, consolidate the
  sprawl, and pipe it into my own observability stack."
- **Lead value — observability & audit.** Every client→MCP transaction converges
  in one process (shared daemon = single collection point), so it can be
  captured, attributed per client, and exported over OTLP to the user's own
  Grafana family. No single client gives this cross-server, cross-agent view; the
  whole architecture is purpose-built for it, and agent non-determinism makes it
  *necessary*, not merely nice.
- **Supporting values:** **consolidation** (`garmx import` → one entry point),
  **curation** (per-agent profiles → fewer/better tools → better decisions +
  lower token cost — a value axis with no API-gateway analog), **MCP-specific
  safety** (redaction, no token passthrough, elicitation governance).
- **Deliberately NOT now:** enterprise multi-tenant RBAC, being an IdP, competing
  on scale. That is the *expansion path* once there are audit users — leading with
  it would fight the local single-binary design.

**Why this wedge (the critical view).** The value shape of an API gateway carries
over — one control point, offload cross-cutting concerns, decouple consumers from
backend sprawl — but MCP breaks several classic pillars: caching mostly
evaporates, composition moves to the agent, and the consumer is a
non-deterministic LLM, which makes **observability and curation matter more** and
makes "just aggregation" table stakes. GarmX leads with the pillar its
architecture actually wins on.

**Honest risks (kept in view, not hidden):**

- **Crowded category** — aggregation alone is table stakes (many MCP gateways
  exist); the audit/observability lead is the differentiator, not aggregation.
- **Protocol-churn absorption** is valuable now but a **time-limited maintenance
  tax** as MCP stabilizes post-1.0.
- **Value accrues to the human operator,** not the agent — so messaging targets
  the developer-as-operator; the agent only "cares" about getting the right tools
  (which is the curation supporting-value).

Everything below — the protocol map, roadmap, RBAC, async lifecycle, and demo —
serves this positioning: **make the observability plane real, easy to adopt, and
trustworthy.**

---

## Part 1 — The protocol landscape

### 1.1 Origin & history

MCP was created by **Anthropic** and announced as an open standard on
**25 November 2024**. The problem it targets is the "M×N" integration
explosion: every AI host needing a bespoke connector to every tool and data
source. MCP standardizes the middle so any compliant client talks to any
compliant server — "USB-C for AI."

It deliberately reused proven pieces rather than inventing them:

- **JSON-RPC 2.0** as the message envelope (request / response / notification).
- A **client–server** split: a *host* (an IDE, an agent, Claude Desktop) runs
  one or more *clients*, each holding a 1:1 connection to a *server*.
- The **Language Server Protocol (LSP)** playbook — the same "standardize the
  middle so N editors × M languages collapses to N+M" idea and the same
  JSON-RPC lineage.

The spec is **date-versioned**, and its version history is the story of the
protocol maturing from a wire format into an authorization- and
async-aware platform:

| Version | Introduced |
|---------|------------|
| **2024-11-05** | Initial release. stdio + HTTP+SSE transports. |
| **2025-03-26** | **Streamable HTTP** (replacing HTTP+SSE), **OAuth 2.1** authorization, tool annotations, audio content, JSON-RPC batching. |
| **2025-06-18** | **Elicitation**, structured tool output, resource links, **removed** JSON-RPC batching, hardened security (Resource Indicators, RFC 8707). |
| **2025-11-25** | Current **stable**. GarmX's preferred version. |
| **2026-07-28** | **Release candidate** (final due 28 Jul 2026): **stateless protocol core**, **Extensions framework**, **Tasks** (async/long-running), **MCP Apps**, authorization hardening (RFC 9207 `iss`, OIDC `application_type`), a formal **deprecation policy**. |

GarmX already tracks this trajectory: it dropped HTTP+SSE and JSON-RPC
batching precisely because the spec superseded them (see `discovery.md` →
DROPPED).

### 1.2 Ownership & governance

The single most important recent change — it reframes what GarmX is building
against:

- **Originally:** an Anthropic-owned open-source project.
- **9 December 2025:** Anthropic **donated MCP to the Linux Foundation** as the
  founding project of a new **Agentic AI Foundation (AAIF)**, co-announced with
  **Block, OpenAI, and the Linux Foundation**.

MCP is now **vendor-neutral, Linux-Foundation-hosted**, with a two-layer
governance model that is deliberately **decoupled**:

- **AAIF Governing Board** — strategic / resource layer. Platinum founding
  members: **AWS, Anthropic, Block, Bloomberg, Cloudflare, Google, Microsoft,
  OpenAI**.
- **MCP Steering Group** — the *technical* layer: Maintainers → Core
  Maintainers → Lead Maintainers. Core Maintainers own spec direction, can veto
  by majority, and appoint/remove maintainers.

A company cannot buy a spec change through its board seat; changes ship as
merged code via the **SEP** (Specification Enhancement Proposal) process and
earn merit-based influence.

**Implication for GarmX:** it builds on a neutral LF standard backed by every
major model vendor *and* cloud — not an Anthropic-specific format. That
de-risks the foundation GarmX sits on.

### 1.3 Industry adoption

Over 2025 MCP went from "Anthropic's thing" to a de facto standard:

- **OpenAI** — adopted MCP (Mar 2025), Agents SDK support; AAIF platinum.
- **Google / DeepMind** — Gemini MCP support; AAIF platinum.
- **Microsoft** — Copilot Studio, Windows MCP, official **C# SDK**; AAIF
  platinum.
- **AWS** — **AgentCore Gateway**, whose `server___tool` triple-underscore
  prefixing GarmX adopted; AAIF platinum.
- **Clients** — Claude Code, Claude Desktop, Cursor, Zed, Replit, Windsurf,
  Sourcegraph, VS Code, JetBrains, and **OpenCode** (a GarmX first target).
- **Official SDKs** — TypeScript, Python, C#, Java/Kotlin, Go, Rust, Swift.

The adoption shape that matters to GarmX: there are now thousands of MCP
servers and every serious agent host speaks the protocol — which *is* the
"servers scattered across every client's config" problem GarmX's
`import` + aggregation flow targets. A **gateway/registry** category has
formed (MetaMCP, mcp-gateway, AgentCore Gateway, Docker MCP Toolkit); GarmX
sits inside it with a **local-first + audit + OTEL** differentiator.

### 1.4 The wider foundation

- MCP is the **flagship/founding project of the AAIF**, under the **Linux
  Foundation** (the CNCF/Kubernetes/OpenTelemetry umbrella).
- AAIF is chartered to host a **family** of agentic-AI standards, not just MCP —
  expect siblings around agent-to-agent communication, identity, and
  registries.
- Companion efforts already moving: the **official MCP Registry**
  (`registry.modelcontextprotocol.io`, LF-hosted, signed metadata / capability
  declarations / version tracking) and **MCP Server Cards** (a `.well-known`
  URL exposing structured server metadata for discovery without connecting).

**The OTel connection is strategic, not incidental.** OpenTelemetry is also an
LF (CNCF) project. GarmX's "emit OTLP, don't rebuild Grafana" decision means
its observability plane rides a *second* LF standard, aligning GarmX with the
same neutral-standards gravity well as MCP itself. This is a positioning asset
(see Part 4).

---

## Part 2 — Primitive & method map (what GarmX does)

**Status legend:**

- **Synthesized** — GarmX builds an aggregated response; not forwarded verbatim.
- **Pass-through** — routed to one owning upstream (with name rewrite where
  noted).
- **Local** — answered by GarmX itself.
- **Deferred** — consciously postponed for v1; session model keeps the hook.
- **Gap** — not yet designed / never discussed. Candidate for the roadmap.
- **Dropped** — out of scope by decision (see `discovery.md`).

### 2.1 Lifecycle & transport

| Method / concept | Direction | GarmX status | Notes |
|------------------|-----------|--------------|-------|
| `initialize` | client→GarmX→upstreams | **Synthesized** | Version negotiate (client + per-upstream), **merged** capability union. |
| `notifications/initialized` | client→GarmX | **Local** | Session marked ready. |
| `ping` | both | **Local** | Answered locally; also GarmX's upstream health probe. |
| Transport: stdio (client face) | — | **Supported (primary)** | `garmx serve --stdio` shim → shared daemon. |
| Transport: Streamable HTTP (client face) | — | **Supported (secondary)** | Single endpoint, `:9735/mcp`. |
| Transport: stdio (upstream face) | — | **Supported** | Subprocess, id-demux, stderr drain. |
| Transport: Streamable HTTP (upstream face) | — | **Supported** | Remote upstreams. |
| Transport: HTTP+SSE (2024-11-05) | — | **Dropped** | Superseded by Streamable HTTP. |
| JSON-RPC batching | — | **Dropped** | Removed from MCP in 2025-06-18. |

### 2.2 Tools (server primitive)

| Method | GarmX status | Notes |
|--------|--------------|-------|
| `tools/list` | **Synthesized** | Fan-out, merge, **prefix** names `server___tool`, eager page-merge, cache per profile. |
| `tools/call` | **Pass-through** | Split on first `___`, strip prefix, route to owning upstream; audit + WS emit. |
| `notifications/tools/list_changed` (upstream→GarmX) | **Synthesized re-emit** | Refresh upstream, rebuild merged maps, debounce, re-emit to sessions. Confirmed both clients re-fetch within ms. |
| Structured tool output (2025-06-18) | **Pass-through (untyped)** | Flows through `tools/call` result; GarmX does not inspect. **Gap:** not explicitly validated/represented. |
| Tool annotations (2025-03-26) | **Pass-through** | Carried in tool schema; **Gap:** not surfaced in UI or used by profiles. |

### 2.3 Resources (server primitive)

| Method | GarmX status | Notes |
|--------|--------------|-------|
| `resources/list` | **Synthesized** | Fan-out, merge; keep `uri → upstream` ownership map. Not prefixed. |
| `resources/read` | **Pass-through** | Routed by `uri` ownership. |
| `resources/templates/list` | **Synthesized** | Fan-out, merge. |
| `resources/subscribe` | **Gap** | `resources.subscribe` is OR-ed into the merged capability, but subscribe **routing + fan-out is not designed**. Never discussed. |
| `resources/unsubscribe` | **Gap** | Same as above. |
| `notifications/resources/updated` (upstream→GarmX) | **Gap** | No propagation path designed; pairs with subscribe. |
| `notifications/resources/list_changed` | **Gap (implied)** | Arch focuses on `tools/list_changed`; resources variant not specified. Clients re-fetch tools only per captures — low urgency, but undesigned. |

### 2.4 Prompts (server primitive)

| Method | GarmX status | Notes |
|--------|--------------|-------|
| `prompts/list` | **Synthesized** | Fan-out, merge, **prefix** prompt names. |
| `prompts/get` | **Pass-through** | Split prefix, route by name. |
| `notifications/prompts/list_changed` | **Gap (implied)** | Same shape as tools; propagation not specified. Claude Code pulls `prompts/list` at startup only. |

### 2.5 Completion & logging (utility primitives)

| Method | GarmX status | Notes |
|--------|--------------|-------|
| `completion/complete` | **Pass-through** | Routed by the ref (prompt/resource) to its owning upstream. |
| `logging/setLevel` (client→server) | **Gap** | Not designed. Would need fan-out to all upstreams that advertise `logging`. Never discussed. |
| `notifications/message` (upstream log→client) | **Gap** | No forwarding path. Could feed the audit/observability plane rather than the client. Never discussed. |

### 2.6 Server→client requests (client primitives) — the deferred axis

These invert direction: an **upstream** asks the **client** to do something.
GarmX today replies with `-32601` (method not found) and logs. The session
registry threads the "which client originated this" back-reference, so adding
real callback routing is an extension, not a rewrite.

| Method | GarmX status | Notes |
|--------|--------------|-------|
| `sampling/createMessage` | **Deferred** | Neither first client advertises `sampling`. Lowest urgency. |
| `elicitation/create` (2025-06-18) | **Deferred** | **Claude Code advertises `elicitation`.** Highest-urgency deferred item — a real client can already receive these. |
| `roots/list` | **Deferred** | Both clients advertise `roots` (Claude Code with `listChanged`). Requires per-session roots routing. |
| `notifications/roots/list_changed` | **Deferred** | Pairs with roots. |

**Strategic note (revised after the RC):** deferring this axis looked like
GarmX's largest **roadmap debt** — `elicitation` + `roots` are advertised by
real captured clients *today* under the current stable spec. But the 2026-07-28
RC **removes the server→client JSON-RPC request** for elicitation, sampling, and
roots (SEP-2322, "Multi Round-Trip Requests") and folds them back into ordinary
client-initiated request/response. If GarmX bets on the RC shape, the debt
**shrinks** rather than grows. See the deep dive in **Part 3.4**.

### 2.7 Cross-cutting utilities

| Concept | GarmX status | Notes |
|---------|--------------|-------|
| Pagination (cursors) | **Synthesized** | Eager page-merge; GarmX issues **no** client-facing cursor; rejects client cursor with `-32602`. |
| `notifications/cancelled` | **Partial → Phase 7** | Must no-op a cancel for an unknown/finished id (OpenCode emits these for completed calls). Live-cancel forwarding is **folded into the Phase 7 async lifecycle** (`tasks/cancel` for tasks; no-op unknown/finished ids). See Part 3.6. |
| `notifications/progress` + `_meta.progressToken` | **Gap → Phase 7** | Clients send `progressToken` on `tools/call`; upstream progress notifications relay back to the originating session. **Folded into the Phase 7 async lifecycle** (Part 3.6). |
| `_meta` passthrough (e.g. `claudecode/toolUseId`) | **Partial** | Observed in captures; ensure GarmX preserves `_meta` end-to-end. Confirm in tests. |
| Authorization / OAuth 2.1 (Streamable HTTP) | **Deferred** | Tied to the HTTP daemon face; token→profile RBAC waits for it (see `architecture.md` → Profiles). |

### 2.8 Coverage summary

- **Solid:** Tools (full lifecycle), the aggregation core, version negotiation,
  capability merge, pagination, stdio + Streamable HTTP on both faces.
- **Deferred (known):** the entire server→client axis (sampling / elicitation /
  roots), OAuth/RBAC on the HTTP face.
- **Gaps (never discussed) worth a decision:** resource subscribe/update,
  prompts/resources `list_changed` propagation, `logging/*`, live cancellation
  forwarding, progress relay, structured-output/annotation surfacing.

---

## Part 3 — Roadmap overlay

### 3.1 The MCP 2026 direction

The 2026 roadmap commits to four pillars, and the **2026-07-28 RC** maps onto
them:

| Pillar | RC deliverable | What it is |
|--------|----------------|------------|
| **Transport scalability** | **Stateless protocol core** | Server can operate without pinning per-session state to a connection — friendlier to horizontal scaling / serverless. |
| **Agent communication** | **Tasks** (graduating), **MCP Apps** | First-class async/long-running operations; richer interactive app surfaces. |
| **Governance maturation** | **Deprecation policy**, **Extensions framework** | Formal deprecation; standardized way to add capabilities outside the core. |
| **Enterprise readiness** | **Authorization hardening**, **Registry** + **Server Cards** | RFC 9207 `iss` validation, OIDC `application_type`; signed, discoverable server metadata. |

### 3.2 Each item vs GarmX

| Roadmap item | GarmX collision / opportunity | Current stance | Proposed GarmX response |
|--------------|-------------------------------|----------------|-------------------------|
| **Stateless core** | GarmX is a **stateful** aggregator (shared daemon, session registry). Sounds like a conflict; likely **orthogonal** — "stateless" governs the client↔server *session/transport*, not whether a gateway may hold catalog/audit state. | Undiscussed | **Confirm orthogonality.** GarmX can *speak* the stateless client-facing contract while remaining internally stateful. Verify GarmX doesn't rely on transport-pinned state the RC removes. |
| **Tasks (async)** | GarmX is request/response with `id`-correlated demux. Long-running Tasks change the lifecycle it must broker (create → poll → complete/cancel). | **Decided (Part 3.6)** | **Broker-only**; task-id **wrapping**; **union** capability; unified into the **Phase 7** async lifecycle. Reserve the routing seam now, build later. Audit of long-running ops is a differentiator. |
| **MCP Apps** | Interactive app surfaces lean on the **server→client axis** GarmX deferred. | Deferred axis | Ties directly to un-deferring elicitation/roots. Watch; don't build until the axis lands. |
| **Extensions framework** | A sanctioned way to add capabilities. GarmX could expose **GarmX-specific** metadata (audit ids, profile info) as an extension rather than abusing `_meta`. | Undiscussed | **Opportunity.** Model GarmX value-adds as MCP extensions for forward-compatibility. |
| **Deprecation policy** | Governs how GarmX tracks version support windows (it already supports `{2025-11-25, 2025-06-18}`). | Aligned-ish | Adopt the policy to decide when to drop old client versions. Low effort. |
| **Authorization hardening** | Directly hits GarmX's HTTP-daemon token→profile RBAC — currently deferred. RFC 9207 / OIDC `application_type` / RFC 8707 resource indicators. | Deferred | The HTTP face is where GarmX's **real per-agent RBAC** becomes enforceable. Track RC auth requirements as the acceptance criteria for that face. |
| **Registry + Server Cards** | GarmX scoped the public Registry as a *later* discovery source. Roadmap makes it **central**; Server Cards enable discovery-without-connecting. | Later/optional | **Reassess priority.** Registry-as-discovery-source may move from nice-to-have to table stakes. GarmX could also *emit* a Server Card describing its aggregate. |

### 3.3 Derived GarmX roadmap (draft — to refine together)

Ordered by strategic leverage, not effort:

1. **Un-defer the server→client axis, starting with `elicitation` + `roots`** —
   real clients advertise them today; MCP Apps/Tasks deepen the dependency.
2. **Tasks + progress + cancellation as one coherent async story** — the three
   are entangled; auditing long-running ops is a GarmX differentiator.
3. **Confirm stateless-core orthogonality** — a small investigation with large
   directional consequences; do before Streamable-HTTP hardening.
4. **HTTP daemon + OAuth2.1 auth (RC-hardened) → real RBAC** — unlocks the
   token→profile identity story profiles were designed for.
5. **Registry + Server Cards** — as discovery-in and as GarmX-describes-itself
   out; re-rank against roadmap centrality.
6. **Close the small gaps** — resource subscribe/update, `list_changed` for
   prompts/resources, `logging/*` → audit plane, `_meta` end-to-end tests.
7. **Model GarmX value-adds as MCP Extensions** — future-proof the audit/profile
   metadata instead of leaning on `_meta`.

### 3.4 Deep dive: elicitation & the stateless server→client pivot

This is the axis GarmX deferred; the RC changes its *mechanism*, so it deserves
a first-principles look.

**What elicitation is (current stable, 2025-06-18 / 2025-11-25):** a server,
mid-interaction (typically nested inside a `tools/call`), asks the **user** for
structured input **through the client**. Method `elicitation/create`
(a server→client request). `params` = `message` (human prompt) +
`requestedSchema`, a **restricted flat** JSON Schema: primitive properties only
(`string`, `number`/`integer`, `boolean`, `enum`) with a few string `format`s
(`email`, `uri`, `date`, `date-time`) — **no nesting, no arrays of objects.**
The response carries an `action` ∈ {`accept`, `decline`, `cancel`} plus
`content` matching the schema.

- **Purpose:** fill missing parameters, confirmations ("delete 3 files?"),
  progressive disclosure, simple choices — *without* baking them into the tool's
  input schema up front.
- **Hard rules (spec):** servers **MUST NOT** request sensitive info (passwords,
  tokens) via elicitation; clients **SHOULD** show which server is asking, allow
  decline/cancel at any time, rate-limit, and validate against the schema.
- **Capability:** the **client** advertises `elicitation: {}` at `initialize`
  (Claude Code does; OpenCode does not — see `client-handshakes.md`).

**The RC pivot (2026-07-28, SEP-2322 "Multi Round-Trip Requests"):** removes the
server→client JSON-RPC request for `elicitation`, `sampling`, and `roots`.
Instead the server returns an **`InputRequiredResult` on the original request**:
`resultType: "inputRequired"`, `inputRequests: {…}`, `requestState: "<opaque
base64>"`. The client gathers answers and **re-issues the original call** with
`inputResponses` + the echoed `requestState`; any server instance can resume
because all state rides in the payload. That is precisely what makes the
**stateless core** work — no sticky sessions, no open reverse stream.

**Answers to the three questions this raised:**

1. **Purpose** — structured, non-sensitive, mid-call user input (above).
2. **Does it fan transactions both ways?**
   - **Current stable: yes** — a genuine `upstream → GarmX → client` reverse
     request. GarmX would correlate the upstream's `elicitation/create` to the
     in-flight `tools/call`'s originating session, forward it to that client, and
     relay the response back. That is the deferred server→client axis in full.
   - **RC: no reverse channel.** Elicitation becomes a special **result** of
     `tools/call`, which GarmX already handles as pass-through. Flow:
     `tools/call` → upstream returns `InputRequiredResult` → GarmX returns it to
     the client on the **same id** → client re-issues with
     `inputResponses`+`requestState` → GarmX routes the follow-up to the **same
     upstream**. New work is modest and fits the existing model: (a) **upstream
     affinity** — send the follow-up to the upstream that issued the
     `requestState`; (b) preserve `requestState`/`inputRequests` **opaquely**;
     (c) annotate *which upstream is asking* for audit/UI.
   - **Conclusion:** bet on the RC shape. It converts GarmX's biggest roadmap
     debt into a small, model-consistent feature; don't build the full
     bidirectional callback machinery unless a must-support client stays on the
     old model.
3. **Does it need agent registration in GarmX?**
   - **For routing: no.** Correlation is by session/call (current) or
     `requestState` affinity (RC) — neither needs a registered identity.
   - **Capability honesty needs care:** `elicitation` is a *client* capability,
     but GarmX shares one upstream across many sessions. Advertise `elicitation`
     to an upstream as the **union** (any connected client supports it), then
     per-call route to the originating session and **auto-decline** if *that*
     client lacks it. (Same union philosophy as tool capabilities, inverted
     toward upstreams.)
   - **Registration/identity is a *separate* concern (RBAC/security),** needed
     regardless of elicitation. Don't conflate them: elicitation works without
     identity; RBAC needs it either way.

**GarmX value-add (mediation, not just passthrough):** GarmX sits in the middle
of every elicitation. It can **audit** them, surface "which upstream asked the
user for what," enforce the spec's *no-sensitive-info* rule, rate-limit, and
(RC) mediate URL-mode credential flows. That is a security + observability
differentiator — the same "one vantage point" argument as the audit plane, and
the reason it earns its own roadmap slot: **Phase 7** (interactive tool flows) in
[`../implementation.md`](../implementation.md).

> **Next deep-dive owed: RBAC & security** (Part 6, item 3). Elicitation surfaced
> it but does not depend on it. The real driver is the Streamable-HTTP face,
> where a token→profile identity makes per-agent access control enforceable.

### 3.5 Deep dive: RBAC & security

**The frame in one line:** MCP standardizes **authentication and token
validation**, *not* **authorization policy**. The gap — *which identity may use
which tool* — is exactly what a gateway like GarmX exists to fill. So GarmX's
job splits cleanly: **be a correct OAuth participant** (for standards) and
**own the RBAC + audit layer the spec leaves out** (for value).

#### What MCP standardizes (current, 2025-06-18 / 2025-11-25)

- **Transport-scoped, HTTP-only, OAuth 2.1.** Authorization is defined *only*
  for HTTP transports. **stdio SHOULD NOT use it** — credentials come from the
  environment. → GarmX's stdio face (identity = the `--profile` launch) is
  *spec-aligned*, just unauthenticated by construction.
- **Clean role split.** An MCP server is an OAuth 2.1 **resource server**
  (validates tokens); the **authorization server** (the IdP) is a separate
  entity, out of MCP's scope. The server never runs logins.
- **Discovery:** RFC 9728 Protected Resource Metadata
  (`/.well-known/oauth-protected-resource`) + `WWW-Authenticate` on 401;
  RFC 8414 AS metadata; RFC 7591 Dynamic Client Registration (SHOULD).
- **PKCE MUST. RFC 8707 Resource Indicators MUST** — the client sends a
  `resource` parameter so the token's **audience** binds to one specific MCP
  server; the server **MUST validate it is the intended audience** and reject
  foreign tokens.
- **Token passthrough is explicitly FORBIDDEN.** If a server calls an upstream
  API it acts as that API's OAuth client and obtains a **separate** token; it
  MUST NOT forward the client's token. This is the **confused-deputy**
  mitigation — and the spec names "servers acting as intermediaries to
  third-party APIs" as the danger, which *is precisely what a gateway is.*

#### What the RC (2026-07-28) hardens

| SEP | Change | Why it matters (especially to GarmX) |
|-----|--------|--------------------------------------|
| **2468** | Client MUST validate `iss` on auth responses (RFC 9207) | Mitigates OAuth **mix-up** attacks — *more prevalent in the single-client / many-server shape*, which is exactly the fan-out GarmX embodies. |
| **837** | Client declares OIDC `application_type` in DCR | Stops an AS defaulting a desktop/CLI client to "web" and rejecting its `localhost` redirect. |
| **2350** | Scope accumulation on step-up | Incremental consent — request more scope mid-flow without dropping prior grants. |
| **2352** | Credential binding to the issuing AS | Tokens can't be replayed against a different AS. |
| **990** | ID-JAG identity assertion for enterprise IdP flows | Enterprise-managed authorization — the on-ramp for IdP-governed access. |
| stateless core | every HTTP request carries + revalidates the bearer token | No session to lean on → validate per request (already the spec rule). |

#### GarmX is a confused deputy by design — the non-negotiable rules

GarmX is **both** an OAuth **resource server** (client-facing) **and** an OAuth
**client** (upstream-facing) — the exact intermediary shape the spec warns
about. So:

- **Client face (resource server):** publish RFC 9728 metadata; answer 401 with
  `WWW-Authenticate`; validate every bearer token's **signature, expiry, issuer
  (`iss`), and audience (= GarmX's own canonical URI)**; reject tokens minted for
  anything else. Never be an authorization server.
- **Upstream face (OAuth client):** for a protected remote upstream, run GarmX's
  *own* auth flow and hold a token whose audience is *that upstream*. **Never**
  forward the client's token. Per-hop tokens; per-registered-client consent if
  proxying DCR.

#### DECIDED — identity model: **terminate at GarmX (Model A)**

GarmX holds each upstream's credentials; client sessions share one upstream
connection; **RBAC is enforced at GarmX** (token → role → profile decides what a
client may see/call). The upstream sees only "GarmX." This fits the shared-daemon
design, preserves the one-shared-upstream optimization, and makes GarmX **the
trust boundary and the single audited attribution point** — the natural fit for
the audit/observability value.

**Model B — propagate identity to the upstream** (so *it* enforces per-user
permissions) — is **deferred, demand-gated.** If built, it MUST use **OBO /
token exchange** (mint a *new* upstream token carrying the user's subject), never
raw passthrough, and it breaks the shared-upstream connection. Not built
speculatively; revisit only if adopter usage/issues justify it.

#### DECIDED — GarmX is a **resource server, never an IdP**

- **Baseline validation is IdP-agnostic.** OIDC discovery + JWKS signature check
  + `iss`/`aud`/expiry validation works with **any** compliant IdP (Okta, Entra
  ID, Auth0, Google, Keycloak) with **no per-IdP code**. The standard *is* the
  integration — the first increment is one generic OIDC validator, not adapters.
- **Per-IdP adapters come later, demand-driven,** only for (a) **claim/role
  mapping** (each IdP puts roles/groups in a different claim, and token → role
  needs to know where to look) and (b) **discovery/DCR quirks**.
- **Enterprise ID-JAG / OBO token-exchange** (SEP-990) is a much-later overlay,
  relevant only if Model B ever happens.
- **Sequence:** generic OIDC validation → per-IdP claim-mapping adapters as
  demand appears → enterprise token-exchange much later.

#### DECIDED — stdio: no auth; exclusivity is **configuration governance**

Over stdio there is no runtime principal (spec: stdio SHOULD NOT use OAuth);
identity is the launch/config (`--profile`). Two things can sit beside GarmX in
a client, and neither is a runtime control GarmX can impose:

1. **The client's native/built-in tools** (file edit, bash, web fetch) — not MCP
   at all, invisible to the protocol and to GarmX. Out of scope; not a gateway's
   concern.
2. **Other MCP servers configured directly** (a "sidecar" server added straight
   to the client config) — these **bypass GarmX entirely.** GarmX is just one
   subprocess the client launched; it has **no visibility into its siblings** and
   cannot technically block them.

Therefore **"route everything through GarmX" is a configuration policy, not a
wall GarmX erects.** The lever is the onboarding flow: `garmx import` sweeps a
client's existing direct servers into GarmX, then the client is **repointed at
GarmX as its sole MCP entry.** In a solo/dev setting that is discipline; in an
org it is enforced via **managed client config / MDM**. GarmX supplies the single
audited entry point and the migration tool; the *environment* enforces
exclusivity. Genuinely enforceable identity exists only on the **HTTP face**.

#### What GarmX should provide

**For standards (compliance — table stakes):**

- OAuth 2.1 resource server on the HTTP face: RFC 9728 metadata,
  401/`WWW-Authenticate`, PKCE-terminated flows via an external AS.
- **Audience + `iss` validation** on inbound tokens (RC SEP-2468); reject
  foreign-audience tokens.
- **No token passthrough**; per-upstream OAuth-client tokens; confused-deputy-safe.
- Generic OIDC validation (JWKS + discovery); RFC 7591 DCR and OIDC
  `application_type` (SEP-837) when GarmX is a client to a remote upstream.

**For value (the gap the spec leaves — differentiators):**

- **RBAC the spec doesn't define:** token → role → profile; per-role
  server/tool allow-deny; immediate policy propagation (no redeploy).
- **Identity-attributed audit:** every tool call tied to an authenticated
  principal — the "who did what, to which upstream, with what result" record that
  underpins SOC2/compliance. GarmX's audit plane + identity is the *only* layer
  positioned to produce it.
- **Elicitation mediation** (Part 3.4): govern what upstreams ask users; block
  secret-fishing.
- **Centralized credential custody:** one vetted, redacted, rotated store of
  upstream secrets instead of N clients each holding them.

#### Demo note

To show RBAC **without** making GarmX an IdP, the Phase 9 demo ships a
lightweight IdP container (Keycloak or Dex) as the auth server, so
`docker compose up` can demonstrate token → role → profile against a real,
external AS. (Tracked in Part 5.)

> **Enforcement gate:** RBAC becomes real only with HTTP-face token identity →
> **Phase 5**. The RC auth-hardening items are that phase's acceptance criteria;
> enterprise IdP (ID-JAG, SEP-990) is a later overlay.

### 3.6 Deep dive: Tasks & the async lifecycle

**What Tasks is:** the "call now, fetch later" pattern for long-running / async
tool work (deep research, builds, batch jobs) that can't fit synchronous
request/response — especially under the stateless core. It is an **optional,
independently-versioned extension** (SEP-1686 → RC SEP-2663), **not core**, so
for GarmX it is a **value** decision, not a standards obligation. (Experimental
in the 2025-11-25 core; moved to an extension in 2026-07-28.)

**How it works (protocol):**

- **Server-directed creation:** the client advertises the Tasks extension; the
  **server** decides a `tools/call` should run as a task and returns a
  **`CreateTaskResult`** (task ID, status `working`, timestamps) instead of an
  immediate result.
- The client drives it by ID: `tasks/get` (status), `tasks/result` (final
  output), `tasks/update`, `tasks/cancel`.
- **State machine:** `working` → terminal `completed` / `failed` / `cancelled`;
  terminal is final; results are retrievable for a server-defined duration.
- **`tasks/list` was removed** — it can't be scoped safely without sessions
  (the stateless core actively shed the one enumeration-style operation).
- **Composes with input-required** (SEP-2322): a task can pause to elicit input,
  then resume — which is why GarmX brokers both as one lifecycle (Part 3.4).

**GarmX position — broker, not producer.** Creation is server-directed, so GarmX
relays upstream task decisions and never turns its own synthesized methods
(`tools/list`, etc.) into tasks.

**DECIDED:**

- **Broker-only.** GarmX keeps only an id→upstream routing handle + audit rows;
  it **never re-implements the task state machine.** The upstream owns task
  state. Accepted edge: an upstream crash mid-task loses the task (state lived
  upstream) → GarmX surfaces `failed`; GarmX is not durable across upstream
  restarts.
- **Task-id routing = wrapping (prefix), not an ownership map.** GarmX wraps the
  upstream's opaque task ID with the server name so `tasks/get|result|cancel`
  route reversibly with **no state** — consistent with `server___tool` tool
  prefixing. *Caveat to verify:* confirm real clients treat task IDs as opaque
  (don't parse or exact-constrain them) before fixing the wrap format.
- **Capability = union, per-upstream version.** Advertise the Tasks extension to
  clients if **any** upstream supports it; track each upstream's extension
  version (adds an axis to the version matrix).
- **One unified phase.** Tasks + elicitation(input-required) + progress +
  cancellation are one lifecycle → built as a single **"async & interactive tool
  lifecycle"** phase (**Phase 7**), Tasks as the spine, elicitation as its
  input-required branch. **Reserve the routing seam now; build the feature
  later** (optional extension; first tools are synchronous).

**Value:** long-running tasks are where centralized observability shines — GarmX
audits creation, tracks `working → terminal` transitions, records real duration,
and attributes it (a long-lived OTel span per task): "all in-flight long-running
MCP operations across every upstream, in one view." It also standardizes the
previously-Gap progress relay and live cancellation (Part 2.7).

---

## Part 4 — LF standards alignment (OTel and beyond)

GarmX intentionally rides **two** Linux Foundation standards. Keeping them
coherent is a positioning strategy, not just plumbing.

- **MCP (AAIF / LF)** — the protocol GarmX aggregates.
- **OpenTelemetry (CNCF / LF)** — how GarmX exports what it observes. "Emit
  OTLP, don't rebuild Grafana" (see `architecture.md` → Observability).

Signal mapping (already decided) — restated here as the alignment anchor:

| OTel signal | GarmX source | Typical backend |
|-------------|--------------|-----------------|
| Traces | each `tools/call` as a span: client → garmx → upstream | Tempo |
| Metrics | call/error counters + latency histograms, bounded labels | Prometheus |
| Logs | redacted audit payloads | Loki |

Strategic threads to pursue in this doc over time:

- **Semantic conventions.** OTel is standardizing **GenAI / agent** semantic
  conventions. GarmX should map its span/attribute names to them
  (`gen_ai.*`, tool-call attributes) so its telemetry is portable, not
  bespoke. **Open — never discussed.**
- **Redaction before the export fork** stays non-negotiable as new signals are
  added (RC Tasks will produce longer-lived spans carrying more argument data).
- **Watch AAIF siblings.** If AAIF standardizes agent identity or an agent-to-
  agent protocol, GarmX's RBAC/attribution story should align with it rather
  than inventing a parallel scheme.

---

## Part 5 — Demo stack (fast-launch showcase)

Goal: a `docker compose up` that a potential adopter runs in minutes and
immediately sees GarmX's value — **aggregation + audit + OTEL observability** —
with real MCP servers and real agent traffic.

### 5.1 Components

| Layer | Component | Purpose |
|-------|-----------|---------|
| **Gateway** | `garmx` (Streamable HTTP face) | The star. Aggregates upstreams; exports OTLP; serves the UI on `:9735`. |
| **Upstream MCP servers** | 2–3 reference servers (e.g. filesystem, `everything`/test, fetch or time) | Show multi-server aggregation + name prefixing. Mix stdio (in-container) and a remote Streamable-HTTP server (separate container) to exercise both upstream transports. |
| **Backing service** | Postgres (for a `postgres` MCP server) | Makes a tool call do something real and traceable. |
| **Observability** | `grafana/otel-lgtm` (all-in-one: OTel Collector + Prometheus + Tempo + Loki + Grafana) | **Fast path** — one container gives the full LGTM stack. Break out into discrete services later if needed. |
| **Client / agent** | (a) a scripted **traffic-generator agent** (no API key) + (b) optional real client (OpenCode → local llama.cpp, or Claude Code) | (a) guarantees the demo shows data with zero secrets; (b) shows a real agent driving GarmX. |

### 5.2 Sketch

```yaml
# docker-compose.yml (sketch — to be fleshed out)
services:
  garmx:
    image: garmx:latest
    command: ["serve", "--http", "--config", "/etc/garmx/demo.jsonc"]
    ports: ["9735:9735"]      # UI + Streamable HTTP MCP endpoint
    environment:
      GARMX_OTLP_ENDPOINT: "http://lgtm:4317"
      GARMX_OTLP_SIGNALS: "metrics,traces,logs"   # demo: opt-in all
    depends_on: [lgtm, mcp-remote, postgres]

  # All-in-one OTel + Prometheus + Tempo + Loki + Grafana
  lgtm:
    image: grafana/otel-lgtm:latest
    ports: ["3000:3000"]      # Grafana
    # OTLP in on 4317 (gRPC) / 4318 (HTTP)

  # Example REMOTE upstream (exercises Streamable HTTP upstream face)
  mcp-remote:
    image: example/mcp-everything-http:latest

  postgres:
    image: postgres:17
    environment: { POSTGRES_PASSWORD: demo }

  # Zero-secret synthetic agent: drives tool calls through GarmX so the
  # dashboards light up without any model API key.
  demo-agent:
    image: garmx-demo-agent:latest
    environment:
      GARMX_URL: "http://garmx:9735/mcp"
    depends_on: [garmx]
```

Notes / open decisions:

- **stdio upstreams inside the garmx container** vs **remote HTTP upstreams as
  peer containers** — the demo should show *both* to prove both upstream
  transports. `demo.jsonc` seeds them (config-seed path).
- **`grafana/otel-lgtm` first** for speed; a "production-ish" variant that
  breaks out Collector/Prometheus/Tempo/Loki/Grafana can be a second compose
  file.
- **Zero-secret by default.** The synthetic `demo-agent` must produce
  representative traffic with no API keys, so the demo always "just works." Real
  clients (OpenCode/Claude Code) are an opt-in overlay.
- **Pre-built Grafana dashboard** for GarmX spans/metrics should ship with the
  demo so value is visible on first load (ties to Part 4 semantic conventions).
- **Auth demo (RBAC):** ship a lightweight IdP container (Keycloak or Dex) as the
  external AS so the demo shows token → role → profile **without GarmX ever being
  an IdP** (see Part 3.5). Only needed once the Phase 5 HTTP face + Phase 8
  export exist, so this is the fuller demo, not the first cut.

### 5.3 Open questions for the demo stack

- Which reference upstream servers best showcase aggregation *and* audit value
  (something read-only + something with side effects + something remote)?
- Does the synthetic agent generate *scripted* traffic or a small local model
  loop (the user runs a local llama.cpp Qwen via OpenCode — reuse it)?
- One compose file (fast) or a layered set (fast demo + realistic topology)?

---

## Part 6 — Open strategic questions (the living edge)

Consolidated from above; these shape the GarmX roadmap. Items marked
**RESOLVED / DECIDED** are settled and kept for the record; items marked
**TO DISCUSS** are the live agenda — the next conversations to have.

1. **Stateless core — RESOLVED (orthogonal).** GarmX's value (centralized
   audit, logging, OTEL across upstreams) sits *beside* the MCP transactions it
   proxies; the RC's statelessness governs the client↔server session/transport,
   not whether a gateway holds domain state. One guardrail: the HTTP face's
   `Mcp-Session-Id` handling must not assume a session is pinned to a connection.
2. **Server→client axis — direction set (bet on the RC).** Support the RC's
   multi-round-trip `InputRequiredResult` model (upstream affinity + opaque
   `requestState` passthrough) rather than the old bidirectional callback. Open:
   do we need a temporary bridge for clients still on the stable
   `elicitation/create` request model? (Part 3.4.) Now scheduled as **Phase 7**
   (interactive tool flows) in [`../implementation.md`](../implementation.md).
3. **RBAC & security — CAPTURED & DECIDED (Part 3.5).** Model **A** (terminate
   identity at GarmX) is the default; **B** (OBO propagation) is deferred and
   demand-gated. GarmX is a **resource server, never an IdP** — generic OIDC
   validation first, per-IdP claim-mapping adapters later. stdio stays
   unauthenticated; exclusivity is client-config governance. Enforceable only on
   the HTTP face → **Phase 5**, whose acceptance criteria are the RC
   auth-hardening items (RFC 9207 `iss`, OIDC `application_type`, RFC 8707).
4. **Tasks / async lifecycle — CAPTURED & DECIDED (Part 3.6).** Broker-only (no
   task-state reimplementation); task-id **wrapping**; **union** capability;
   Tasks + elicitation + progress + cancellation unified into **Phase 7**.
   Reserve the routing seam now, build later. Open sub-item: verify clients treat
   task IDs as opaque before fixing the wrap format.
5. **Registry + Server Cards — TO DISCUSS.** The RC makes the official
   LF-hosted Registry + `.well-known` Server Cards central to discovery. Threads:
   (a) use the public Registry as a **discovery source** when adding upstreams
   (browse-and-add) — promote from the current "later"? (b) should GarmX
   **publish its own Server Card** describing the aggregate it exposes? Consider
   signed-metadata / version-tracking implications and how it relates to GarmX's
   local catalog (which is *not* the public Registry — see DROPPED note).
6. **OTel GenAI semantic conventions — TO DISCUSS.** OTel (the other LF standard
   GarmX rides) is standardizing GenAI / agent span + attribute conventions
   (`gen_ai.*`, tool-call attributes). Adopt them for GarmX's spans so its
   telemetry is portable, not bespoke — decide the attribute mapping and how it
   binds into the Phase 8 exporter. (Part 4.)
7. **Extensions framework — TO DISCUSS.** The RC sanctions a way to add
   capabilities outside the core. Should GarmX express its value-adds (audit ids,
   profile/attribution metadata) as a **named MCP extension** rather than leaning
   on `_meta`? Forward-compat + interop upside; scope and naming TBD.
8. **Demo stack — SCHEDULED.** Now tracked as **Phase 9** in
   [`../implementation.md`](../implementation.md). Remaining decisions
   (reference upstreams, synthetic-agent traffic profile, single vs layered
   compose) stay in Part 5.

---

## Sources

- [Anthropic — Donating MCP & establishing the Agentic AI Foundation](https://www.anthropic.com/news/donating-the-model-context-protocol-and-establishing-of-the-agentic-ai-foundation)
- [GitHub Blog — MCP joins the Linux Foundation](https://github.blog/open-source/maintainers/mcp-joins-the-linux-foundation-what-this-means-for-developers-building-the-next-era-of-ai-tools-and-agents/)
- [MCP — Governance and Stewardship](https://modelcontextprotocol.io/community/governance)
- [MCP Blog — The 2026 MCP Roadmap](https://blog.modelcontextprotocol.io/posts/2026-mcp-roadmap/)
- [MCP Blog — The 2026-07-28 Specification Release Candidate](https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/)
- [MCP — Roadmap](https://modelcontextprotocol.io/development/roadmap)
- [MCP — Specification](https://modelcontextprotocol.io/specification)
