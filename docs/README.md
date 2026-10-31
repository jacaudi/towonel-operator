# towonel-operator documentation

A Kubernetes operator that automates [Towonel](https://towonel.dev) self-hosted tunnels —
provisioning tunnels, deploying agents, and connecting them, declaratively.

New here? Start with the [Quickstart in the root README](../README.md#quickstart), then dig in:

| File | Covers |
|---|---|
| [installation.md](installation.md) | Helm (OCI) install, chart values, manager flags, prerequisites |
| [architecture.md](architecture.md) | The two-resource model, why routing is agent-side, failover, the source layer, DNS handoff |
| [crd-towoneltunnel.md](crd-towoneltunnel.md) | `TowonelTunnel` reference: spec, status (incl. `regionEndpoints`), conditions, lifecycle |
| [crd-towonelagent.md](crd-towonelagent.md) | `TowonelAgent` reference: services, tcp/udp, connectivity, workload, status |
| [source-layer.md](source-layer.md) | Expose a `Service`/`Gateway`/`HTTPRoute` by annotation |
| [connectivity.md](connectivity.md) | Optional iroh NAT-traversal (`spec.connectivity`) |
| [dns.md](dns.md) | Optional public DNS handoff via external-dns `DNSEndpoint` |
| [examples/](examples/) | Runnable sample manifests |

## What's implemented

- **`TowonelTunnel`** reconcile — invite/token lifecycle, authorized-hostname convergence,
  TCP/UDP port reservations against the Towonel user API.
- **`TowonelAgent`** reconcile — renders the agent Deployment + per-agent token Secret +
  `hostname → origin` routing env; rolls on config change.
- **Source layer** — `Service`, `Gateway`, and `HTTPRoute` annotations materialize/own
  agent routing entries.
- **Direct-path connectivity** — optional iroh autodiscover + UDP NodePort.
- **DNS handoff** — `spec.dns.mode: DNSEndpoint` emits an external-dns `DNSEndpoint` per tunnel (see [dns.md](dns.md)).
- **Region endpoints** — `status.regionEndpoints` and the `RegionEndpointsAvailable` condition report live edge IPs per region (see [crd-towoneltunnel.md](crd-towoneltunnel.md)).

## Not yet implemented (designed)

- **Finite-expiry token rotation** and the **operator-key (hub-admin) tier**.

---

[← Back to README](../README.md)
