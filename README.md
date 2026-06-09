# towonel-operator

A Kubernetes operator that automates [Towonel](https://towonel.dev) self-hosted
tunnels — provisioning tunnels, deploying agents, and connecting them, declaratively.

> [!WARNING]
> **Agentically generated.** This codebase was produced through agentic,
> spec-driven development: each feature began as a written design and
> implementation spec, then a coding agent executed the plan under human
> review. Tests, code review, and CI gates apply as they would for any
> project, but the authorship pattern is not a single human contributor —
> keep that in mind when evaluating fit for your environment.

## Status

Scaffolding phase: CRD types (`TowonelTunnel`, `TowonelAgent`) and a manager
with no-op reconcilers; reconcile logic and publishing land in later phases.

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
