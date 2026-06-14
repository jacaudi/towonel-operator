# Architecture

## The problem

[Towonel](https://towonel.dev) exposes services that sit behind NAT / CGNAT / dynamic IPs
without opening inbound ports. Its topology:

- **Hub + Edge** — a public host. Accepts client TLS on `:443`, examines SNI, and forwards
  over QUIC (iroh) to the matching agent. A control plane authorizes hostnames + ports.
- **Agent** — runs where your origin services are reachable. It holds an *invite token*, dials
  the edges **outbound**, and maps `hostname → origin` (and raw TCP/UDP) **locally, via env vars**.

This operator drives the agent/client side from Kubernetes.

## The one fact that shapes everything

In Towonel, the management API only *authorizes* hostnames and reserves ports — the actual
`hostname → origin` routing lives in the **agent's local environment** (`TOWONEL_AGENT_SERVICES`,
etc.), not in any API. **Routing is authored agent-side.** That is why the design splits into two
resources: a tunnel (authorization/control plane) and an agent (routing + workload).

## Two resources

```
Service / Gateway / HTTPRoute (annotated)  ──emits/owns──▶  TowonelAgent ──tunnelRef──▶  TowonelTunnel
        (source layer — optional)                      (workload + routing)        (control plane / Towonel API)
```

- **[`TowonelTunnel`](crd-towoneltunnel.md)** — the control plane. Reconciles a Towonel invite
  (token, authorized hostnames, TCP/UDP port reservations) against the Towonel user API and owns
  the token Secret. *This is "the tunnel."* It **derives** its authorized-hostname set and port
  reservations by aggregating the agents that reference it.
- **[`TowonelAgent`](crd-towonelagent.md)** — a placement-specific workload. References a tunnel,
  renders the agent Deployment + a per-agent token Secret, and carries the `hostname → origin`
  routing. Multiple agents can reference **one** tunnel for failover.

**Why two CRDs.** An agent is placement-specific — it runs where its origins are reachable.
Several agents (different clusters / network zones) can connect to one tunnel, each exposing the
origins reachable from its location; the hub fails traffic over between them. The tunnel is global;
agents are per-placement. One combined CRD couldn't express N-agents-one-tunnel.

## Data + control flow

- **Token flows down** (tunnel → agent): the tunnel creates the invite and writes the token Secret;
  each agent copies it into its own namespace (no cross-namespace Secret mounts; API keys never
  reach agent pods — only the invite token does).
- **Hostnames/ports round-trip**: an agent *declares* services → the tunnel *authorizes* hostnames
  and *reserves* ports → the agent *consumes* the resolved public ports. Every step is non-blocking,
  so the system is eventually consistent and deadlock-free.
- **Config changes roll the Deployment.** The agent reads everything from env vars, so a routing or
  connectivity change is a Deployment rollout, triggered by a config hash.

## Failover, not sharding

Towonel routing is **tenant-global**: the hub pairs every authorized hostname with the *entire*
live-agent set for that tunnel. So multiple agents on one tunnel are **failover replicas serving the
same hostname set** — not a way to shard different hostnames across agents (an agent configured with a
divergent hostname subset would clobber the others). Within a cluster there is one authoritative agent
per tunnel; additional replicas are full-set copies (`spec.workload.replicas`) or per-cluster agents
referencing the same tunnel.

## The source layer (optional dogfooding path)

Instead of hand-writing a `TowonelAgent`, you can annotate a `Service`, `Gateway`, or `HTTPRoute`;
the operator materializes and owns the equivalent routing entries on a per-tunnel default agent (or a
named one). The three sources form an exposure gradient: a **Service** is direct (origin is the
Service's own `ClusterIP:port`); an **HTTPRoute** forwards through its parent Gateway and is selective
(only the route's own hostnames are authorized); a **Gateway** forwards through that gateway and is
broad (all of its listener hostnames). See [source-layer.md](source-layer.md). This mirrors the
[`cloudflare-operator`](https://github.com/jacaudi/cloudflare-operator) source→primitive pattern.

## Relationship to cloudflare-operator

This operator is the Towonel analog of the author's `cloudflare-operator`. The key difference is the
one above: Cloudflare pushes ingress rules to a remote-config API and the connector is dumb (routing
lives API-side), whereas Towonel's routing is inherently agent-local — which is exactly why the tunnel
and agent are split here.
