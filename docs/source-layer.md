# Source layer (annotations)

Instead of hand-authoring a [`TowonelAgent`](crd-towonelagent.md), annotate a `Service`,
`Gateway`, or `HTTPRoute`. The operator materializes and **owns** the equivalent routing entries
on a per-tunnel agent — exactly as if you had written them yourself. This is the optional
dogfooding path; the CRs remain the source of truth.

Examples: [`04-source-service-simple.yaml`](examples/04-source-service-simple.yaml),
[`05-source-service-multiport.yaml`](examples/05-source-service-multiport.yaml),
[`06-source-gateway-httproute.yaml`](examples/06-source-gateway-httproute.yaml).

## Annotation vocabulary

All keys are under `towonel.io/`. Opt in with `tunnel`, point at a tunnel with `tunnel-ref`.

| Annotation | Applies to | Meaning |
|---|---|---|
| `towonel.io/tunnel` | all | Opt-in. Truthy: `true`/`yes`/`enable`/`enabled`; falsy: `false`/`no`/`disable`/`disabled` (case-insensitive). |
| `towonel.io/tunnel-ref` | all | Target tunnel as `<namespace>/<name>`. |
| `towonel.io/agent-ref` | all | Optional. Target a specific agent by name. See "agent targeting" below. |
| `towonel.io/hostname` | Service | HTTPS hostname to expose (Service shim). |
| `towonel.io/origin` | Service | Override origin `host:port` (defaults to the Service's `ClusterIP:port`). |
| `towonel.io/edge-tls-mode` | Service | `passthrough` (default) or `terminate`. |
| `towonel.io/protocol` | Service | `tcp` or `udp` — emit a raw L4 service instead of HTTPS. |
| `towonel.io/gateway-service` | Gateway | **Required** on an opted-in Gateway — the proxy Service the agent forwards to (not derivable from the Gateway object). |
| `towonel.io/dns-record` | all | **Reserved / inert** — intended to opt a hostname out of the (unimplemented) DNS handoff. See [dns.md](dns.md). |

### Multi-exposure on one Service (port-name-scoped keys)

A single flat `protocol` key can't express HTTP **and** raw TCP on one Service. Use
port-name-scoped keys `towonel.io/<portName>.<suffix>`, where `<portName>` matches a
`spec.ports[].name`:

| Suffix | Meaning |
|---|---|
| `.hostname` | Expose that port as HTTPS with the given hostname. |
| `.tcp` / `.udp` | Expose that port as raw TCP/UDP (truthy value). |
| `.public-port` | Pin the public listen port for that raw service. |

The operator folds all referenced ports into one emitted agent (`services[]` + `tcp[]`/`udp[]`);
unreferenced ports are not exposed. For anything more complex, author the `TowonelAgent` directly.

## Source kinds

- **`Service`** — the shim. `hostname` (HTTPS) or `protocol` (raw), origin defaults to the
  Service's `ClusterIP:port`. Multi-port via the scoped keys above.
- **`Gateway`** — opt in on the `Gateway`; hostnames are derived from its **listener hostnames**.
  `towonel.io/gateway-service: [<ns>/]<name>[:<port>]` is **required** (the in-cluster proxy
  Service to forward to — not derivable from the Gateway). Each listener hostname is emitted as a
  service pointing at that proxy. Mirrors the cloudflare-operator Gateway-source pattern.
- **`HTTPRoute`** — hostnames come from `spec.hostnames`; the route forwards through its **parent
  Gateway** (origin = that Gateway's `towonel.io/gateway-service` proxy). Only the route's
  `spec.hostnames` are authorized — this is the **selective** option. The parent Gateway carries
  `gateway-service` as data and does **not** need `towonel.io/tunnel`. If a route's `parentRefs`
  resolve to more than one distinct proxy, the operator emits an `AmbiguousGateway` Event and skips
  (no silent first-wins).

> **Origins and TLS (important).** The emitted `origin` is the target Service's **`ClusterIP:port`**
> — *not* its cluster-DNS name (it updates if the Service's ClusterIP changes). Source-emitted
> services carry the default `edgeTLSMode: passthrough`: the Towonel edge peeks the SNI and forwards
> raw TLS, so the **origin must terminate TLS**. For an annotated **Service** the origin is the
> Service itself, so that Service must terminate TLS. For an **HTTPRoute** the origin is its parent
> Gateway's proxy, which terminates TLS for the route's hostnames. For a plaintext HTTP origin you
> want `edgeTLSMode: terminate` (the edge terminates with an on-demand cert and forwards plaintext),
> which the Service source can't set — author a `TowonelAgent` directly for that.

> **SNI / DNS.** Towonel routes by the SNI the client sends, and a CNAME chain does **not** rewrite
> it. Every hostname clients actually use must be authorized — i.e. appear as a Gateway listener
> hostname or an HTTPRoute hostname. A wildcard listener (`*.example.com`) covers a whole zone *if*
> Towonel's hub accepts wildcard hostnames (verify upstream); otherwise enumerate each hostname.

## Agent targeting

- **No `agent-ref`** → the operator's single **default agent** for that tunnel, auto-created in the
  tunnel's namespace (or the `--agent-namespace` / `agentNamespace` override). One default agent per
  tunnel — consistent with Towonel's tenant-global routing.
- **`agent-ref` → an operator-owned agent** → the operator writes the routing onto it.
- **`agent-ref` → a user-owned agent** (no operator ownership label) → **observe-only**: the operator
  validates and emits advisory Events but never mutates a hand-authored agent. It will **never
  auto-create** a named agent.

## How contributions are written

Each source contributes by applying its routing onto the agent's **spec** under a per-source field
manager (server-side apply, no force-ownership), so two sources can co-own disjoint entries while a
duplicate-hostname conflict surfaces as an Event. Entries are pruned strictly after the apply when a
source's desired set shrinks, and the agent reconciler renders env from the spec unchanged.

## Validation feedback

The operator cannot write status onto user-owned `Service`/`Gateway`/`HTTPRoute` objects, so
malformed annotations surface only as **`InvalidAnnotationValue` (and similar) Events** on the source
object — `kubectl describe` it to see them. This is the deliberate reason to keep annotations to the
simple cases and push heterogeneous exposure to the CR.
