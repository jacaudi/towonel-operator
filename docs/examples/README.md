# Example custom resources

A story-ordered tour of how you expose a service through Towonel, laid out as an
**Explicit × Implicit** by **Service / Gateway / HTTPRoute** matrix. Pair these with the
reference docs: [TowonelTunnel](../crd-towoneltunnel.md),
[TowonelAgent](../crd-towonelagent.md), [source layer](../source-layer.md).

**Explicit** = you hand-author the `TowonelAgent` CR. **Implicit** = you annotate a
`Service`/`Gateway`/`HTTPRoute` and the operator crafts and owns the agent's routing entries
for you (the dogfooding path) — exactly as if you had hand-authored them.

The three sources form a gradient of how the origin is reached:

- **Service** → **direct**: origin is the Service's own ClusterIP:port.
- **Gateway** → **broad through the gateway**: every listener hostname rides through the
  gateway's in-cluster proxy.
- **HTTPRoute** → **selective through the gateway**: only the route's hostnames ride through the
  parent gateway's proxy.

## Files

`01-tunnel.yaml` is the shared prerequisite — every other example references it.

| File | Quadrant | What it shows |
|---|---|---|
| [`01-tunnel.yaml`](01-tunnel.yaml) | prerequisite | `TowonelTunnel` — control plane (invite/token, hostnames, ports) + sample reconciled status |
| [`02-explicit-service.yaml`](02-explicit-service.yaml) | Explicit × Service | Hand-authored `TowonelAgent`; one `services[]` entry whose origin is a Service ClusterIP:port (direct) |
| [`03-explicit-gateway-wildcard.yaml`](03-explicit-gateway-wildcard.yaml) | Explicit × Gateway | Hand-authored `TowonelAgent`; a wildcard `*.example.com` → the gateway proxy ClusterIP:443 (broad) |
| [`04-explicit-httproute.yaml`](04-explicit-httproute.yaml) | Explicit × HTTPRoute | Hand-authored `TowonelAgent`; specific hostnames → the gateway proxy ClusterIP:443 (selective) |
| [`05-implicit-service.yaml`](05-implicit-service.yaml) | Implicit × Service | Annotate a `Service`; origin defaults to its own ClusterIP (direct) |
| [`06-implicit-gateway-wildcard.yaml`](06-implicit-gateway-wildcard.yaml) | Implicit × Gateway | Annotate a `Gateway` with a `*.example.com` listener + `towonel.io/gateway-service` (broad) |
| [`07-implicit-httproute.yaml`](07-implicit-httproute.yaml) | Implicit × HTTPRoute | Annotate an `HTTPRoute`; forwards through its **parent Gateway's** proxy (selective) |

## Reading order

`02`/`05`, `03`/`06`, and `04`/`07` are the explicit/implicit pairs for each source. Read a pair
together to see the same routing expressed two ways: hand-authored on the left, operator-crafted
from annotations on the right.

The HTTPRoute pair (`04`/`07`) demonstrates the **selective-through-gateway** model: traffic for
specific hostnames forwards through the parent Gateway's proxy (`towonel.io/gateway-service`),
which terminates TLS and applies the route's own rules. In `07` the parent Gateway carries
`gateway-service` as **data only** and is **not** `towonel.io/tunnel`-annotated, so only the
HTTPRoute's hostnames are authorized — contrast `06`, where the Gateway **is** tunnel-annotated
and the whole listener zone is exposed (broad).

## TLS

`edgeTLSMode` (CR field) / `towonel.io/edge-tls-mode` (annotation) controls TLS at the edge:

- **`passthrough`** (default) — the Towonel edge peeks the SNI and forwards the **raw TLS** stream;
  the **origin** terminates. The origin must speak TLS.
- **`terminate`** — the edge terminates with an on-demand cert and forwards **plaintext** to the
  origin. Use this for a plaintext HTTP backend.

> **SNI:** Towonel routes by the SNI the client sends, and a CNAME chain does not rewrite SNI.
> Every hostname clients actually use must be authorized on the tunnel — a Gateway listener
> hostname (broad) or an HTTPRoute hostname (selective).

> **DNS:** these examples expose services and reserve ports, but the operator does **not**
> create public DNS records — DNS handoff (`spec.dns`) is designed but not yet implemented
> (see [../dns.md](../dns.md)). Point your DNS at the edge addresses in the tunnel's status
> yourself for now.
