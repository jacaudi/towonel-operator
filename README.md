# towonel-operator

A Kubernetes operator that automates [Towonel](https://towonel.dev) self-hosted
tunnels — provisioning tunnels, deploying agents, and connecting them, declaratively.

> ⚠️ **Early development.** The Towonel API client (`internal/towonel`) is implemented;
> the controllers are in progress. APIs (`towonel.io/v1alpha1`) may change.

## What it does

Towonel exposes services behind NAT/CGNAT without inbound ports. This operator drives
that from Kubernetes via two resources:

- **`TowonelTunnel`** — the control plane. Reconciles a Towonel invite (token,
  authorized hostnames, TCP/UDP port reservations) against the Towonel API and owns
  the token Secret.
- **`TowonelAgent`** — a placement-specific workload that references a tunnel and
  renders the agent Deployment plus its `hostname → origin` routing. Multiple agents
  can share one tunnel for failover.

## How it works

Annotate a `Service`, `Gateway`, or `HTTPRoute` — or author the CRs directly — and the
operator creates the tunnel and the agent. Public DNS is handed off via the standard
external-dns `DNSEndpoint` CRD, so any DNS provider works.

See [`examples/`](examples/) for sample resources.

## License

MIT
