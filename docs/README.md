# towonel-operator documentation

A Kubernetes operator that automates [Towonel](https://towonel.dev) self-hosted tunnels —
provisioning tunnels, deploying agents, and connecting them, declaratively.

New here? Start with the [Quickstart in the root README](../README.md#quickstart), then dig in:

| Doc | What it covers |
|---|---|
| [Installation](installation.md) | Helm (OCI) install, chart values, manager flags, prerequisites |
| [Architecture](architecture.md) | The two-resource model, why routing is agent-side, failover, the source layer |
| [`TowonelTunnel` reference](crd-towoneltunnel.md) | Control-plane CRD: spec, status, conditions, lifecycle |
| [`TowonelAgent` reference](crd-towonelagent.md) | Workload + routing CRD: services, tcp/udp, connectivity, workload, status |
| [Source layer (annotations)](source-layer.md) | Expose a `Service`/`Gateway`/`HTTPRoute` by annotation |
| [Direct-path connectivity](connectivity.md) | Optional iroh NAT-traversal (`spec.connectivity`) |
| [DNS](dns.md) | Public DNS handoff — **designed, not yet implemented** |
| [Examples](examples/) | Runnable sample manifests |

## What's implemented

- **`TowonelTunnel`** reconcile — invite/token lifecycle, authorized-hostname convergence,
  TCP/UDP port reservations against the Towonel user API.
- **`TowonelAgent`** reconcile — renders the agent Deployment + per-agent token Secret +
  `hostname → origin` routing env; rolls on config change.
- **Source layer** — `Service`, `Gateway`, and `HTTPRoute` annotations materialize/own
  agent routing entries.
- **Direct-path connectivity** — optional iroh autodiscover + UDP NodePort.

## Not yet implemented (designed)

- **DNS handoff** (`spec.dns` / external-dns `DNSEndpoint`) — see [dns.md](dns.md).
- **Finite-expiry token rotation** and the **operator-key (hub-admin) tier**.
