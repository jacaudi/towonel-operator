# Source layer (annotations)

Instead of hand-authoring a [`TowonelAgent`](crd-towonelagent.md), annotate a `Service`,
`Gateway`, or `HTTPRoute`. The operator materializes and **owns** the equivalent routing entries
on a per-tunnel agent — exactly as if you had written them yourself. This is the optional
dogfooding path; the CRs remain the source of truth.

Examples: [`05-implicit-service.yaml`](examples/05-implicit-service.yaml),
[`06-implicit-gateway-wildcard.yaml`](examples/06-implicit-gateway-wildcard.yaml),
[`07-implicit-httproute.yaml`](examples/07-implicit-httproute.yaml).

## Annotation vocabulary

All keys are under `towonel.io/`. Opt in with `tunnel`, point at a tunnel with `tunnel-ref`.

| Annotation | Applies to | Meaning |
|---|---|---|
| `towonel.io/tunnel` | all | Opt-in. Truthy: `true`/`yes`/`enable`/`enabled`; falsy: `false`/`no`/`disable`/`disabled` (case-insensitive). |
| `towonel.io/tunnel-ref` | all | Target tunnel as `<namespace>/<name>` (bare `<name>` resolves in the source's namespace). Optional only when exactly one `TowonelTunnel` exists — see "Tunnel targeting" below. |
| `towonel.io/agent-ref` | all | Optional. Target a specific agent by name. See "agent targeting" below. |
| `towonel.io/hostname` | Service | HTTPS hostname to expose (Service shim). |
| `towonel.io/origin` | Service | Override origin `host:port` (defaults to the Service's `ClusterIP:port`). |
| `towonel.io/edge-tls-mode` | Service | `passthrough` (default) or `terminate`. |
| `towonel.io/protocol` | Service | `tcp` or `udp` — emit a raw L4 service instead of HTTPS. |
| `towonel.io/gateway-service` | Gateway | **Required** on an opted-in Gateway — the proxy Service the agent forwards to (not derivable from the Gateway object). |
| `towonel.io/auto-routes` | Gateway | Opt every **same-namespace** HTTPRoute under this Gateway into tunneling, without annotating each route. Truthy values as `towonel.io/tunnel`. **Distinct** from `towonel.io/tunnel` on a Gateway (which exposes the Gateway's own listeners). Requires `towonel.io/gateway-service`. See "Gateway auto-routes" below. |
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

## Gateway auto-routes (`towonel.io/auto-routes`)

Annotating every `HTTPRoute` gets tedious under a busy Gateway. Set
`towonel.io/auto-routes: "true"` on the **Gateway** to tunnel its **same-namespace**
child routes by default — each route is still free to opt out.

`towonel.io/auto-routes` is a **different key** from `towonel.io/tunnel`. On a Gateway,
`towonel.io/tunnel` means *"expose this Gateway's own listener hostnames"* (gateway-as-source);
`towonel.io/auto-routes` means *"auto-tunnel the HTTPRoutes under this Gateway."* A single
Gateway may carry both.

### Precedence (the route's own annotation always wins)

A route's `towonel.io/tunnel` is authoritative; the Gateway default applies **only** when the
route has no `towonel.io/tunnel` key at all:

| Route `towonel.io/tunnel` | Under an `auto-routes` Gateway | Result |
|---|---|---|
| `"true"` / `yes` / `enable` | any | **Tunneled** (unchanged) |
| `"false"` / `no` / `disable` | any | **Excluded** — explicit per-route opt-out |
| **absent** | enabled | **Tunneled** — inherited Gateway default (new) |
| absent | not enabled | Not tunneled (unchanged) |
| present but unrecognized (e.g. `""`, garbage) | any | Not tunneled — a present key never inherits |

A route can never be force-tunneled against its own explicit `false`.

### Worked example

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: external
  namespace: web
  annotations:
    towonel.io/auto-routes: "true"          # tunnel my same-namespace HTTPRoutes
    towonel.io/gateway-service: envoy:443    # the proxy they route through (required)
spec: { gatewayClassName: kgateway, listeners: [ ... ] }
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata: { name: app, namespace: web }       # no towonel.io/tunnel → inherits → tunneled
spec:
  parentRefs: [{ name: external }]
  hostnames: ["app.example.com"]
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: internal-only
  namespace: web
  annotations: { towonel.io/tunnel: "false" }  # explicit opt-out → excluded
spec:
  parentRefs: [{ name: external }]
  hostnames: ["internal.example.com"]
```

The operator records a Normal `AutoSelectedByGateway` Event on each inherited route, so you can
audit what got exposed with `kubectl describe httproute <name>`.

### Cross-namespace auto-routes (`towonel.io/auto-routes-namespaces`)

By default `towonel.io/auto-routes` only auto-selects routes in the Gateway's **own**
namespace. To extend it across namespaces, add an explicit allowlist on the Gateway:

```yaml
metadata:
  annotations:
    towonel.io/auto-routes: "true"
    towonel.io/auto-routes-namespaces: "b,c"   # comma-separated — or "all" (any namespace)
    towonel.io/gateway-service: a/envoy:443
```

An un-annotated HTTPRoute in `b` or `c` that parents this Gateway is now auto-tunneled,
exactly like a same-namespace one. Everything else is unchanged: a route's own
`towonel.io/tunnel` still wins (`"false"` opts out anywhere), and `gateway-service`
is still required.

> ⚠️ **SECURITY — read before using.** Auto-routes exposes a route to the **public**
> Towonel edge **without per-route opt-in**. Cross-namespace auto-routes therefore lets
> a Gateway owner publish **another namespace's** routes to the public internet with
> **only the Gateway owner's consent** — the target namespace cannot refuse short of
> setting `towonel.io/tunnel: "false"` on each route. Use it **only in clusters you fully
> trust** (single-tenant / homelab). **`towonel.io/auto-routes-namespaces: "all"` is a
> blanket escape hatch:** every un-opted-out HTTPRoute that parents the Gateway, in **any**
> namespace, is auto-tunneled — a single annotation with cluster-wide blast radius.
>
> _Future hardening:_ this is unilateral (gateway-side) consent. A target-namespace
> consent signal (a Namespace label or ReferenceGrant-style object) is the intended path
> for multi-tenant clusters and may be added later.

Note (Gateway API): a cross-namespace route only *attaches* to the Gateway if the
listener's `allowedRoutes.namespaces` admits it; otherwise it 404s at the Gateway. The
operator does not check this — see the route's own status for attachment.

### Where auto-routes does NOT apply

- **Cross-namespace routes without an allowlist.** An HTTPRoute in a *different* namespace
  than the Gateway is **not** auto-selected unless the Gateway allowlists that namespace via
  `towonel.io/auto-routes-namespaces` (see [Cross-namespace auto-routes](#cross-namespace-auto-routes-towonelioauto-routes-namespaces) above). Without the allowlist it still needs its own `towonel.io/tunnel: "true"`.
- **Gateway without `towonel.io/gateway-service`.** No origin to forward through → no-op + a
  `GatewayServiceUnspecified` Warning Event on the route.
- **Gateway API disabled** (`--enable-gateway-api=false`). The HTTPRoute/Gateway controllers don't run.
- **An explicit `towonel.io/tunnel: "false"` on the route.** Always excluded, regardless of the Gateway.

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

## Tunnel targeting

Point a source at its tunnel with `towonel.io/tunnel-ref`. Multiple `TowonelTunnel`s
**are** supported; only the omission shortcut requires exactly one.

The `towonel.io/tunnel-ref` *omission* default applies only when exactly one
`TowonelTunnel` exists. With zero or multiple tunnels, set `tunnel-ref` explicitly.
If you rely on the omission default and later add a second tunnel, ref-less sources
become ambiguous: they stop resolving, emit a `TunnelRefMissing` event, and release
their routing until an explicit `tunnel-ref` is set (loud and safe — no mis-routing).

## Agent targeting

- **No `agent-ref`** → the operator's single **default agent** for that tunnel, auto-created in the
  tunnel's namespace (or the `--agent-namespace` / `agentNamespace` override). One default agent per
  tunnel — consistent with Towonel's tenant-global routing.
- **`agent-ref` → an operator-owned agent** → the operator writes the routing onto it.
- **`agent-ref` → a hand-authored agent** → the operator reconciles routing into it by default
  (`spec.mode: Managed`, the default). No label is required. This is how you customize an agent's
  workload (e.g. pin `spec.workload.image`) while letting the operator manage its routing. The
  operator never garbage-collects a hand-authored agent.
- **`agent-ref` → an agent with `spec.mode: ObserveOnly`** → **observe-only**: the operator validates
  and emits advisory Events but never writes routing. Use this to fully self-manage an agent's routing.
- The operator will **never auto-create** a named `agent-ref` agent.

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
