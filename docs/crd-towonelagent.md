# `TowonelAgent` reference

A placement-specific workload that references a [`TowonelTunnel`](crd-towoneltunnel.md) and renders
the agent Deployment + per-agent token Secret. This is where the `hostname → origin` routing lives
(Towonel routing is agent-local). **Namespaced** (`shortName: twa`). N agents may reference one
tunnel for failover.

See [`examples/02-explicit-service.yaml`](examples/02-explicit-service.yaml),
[`examples/03-explicit-gateway-wildcard.yaml`](examples/03-explicit-gateway-wildcard.yaml), and
[`examples/04-explicit-httproute.yaml`](examples/04-explicit-httproute.yaml).

## Spec

| Field | Type | Default | Description |
|---|---|---|---|
| `tunnelRef` | `{name, namespace}` | — | The `TowonelTunnel` to attach to. |
| `services` | []AgentService | — | HTTPS/SNI routes → `TOWONEL_AGENT_SERVICES`. |
| `tcp` | []AgentL4Service | — | Raw TCP services → `TOWONEL_AGENT_TCP_SERVICES` (list keyed by `name`). |
| `udp` | []AgentL4Service | — | Raw UDP services → `TOWONEL_AGENT_UDP_SERVICES` (list keyed by `name`). |
| `relayUrl` | string | — | Optional `TOWONEL_AGENT_RELAY_URL` override. |
| `connectivity` | object | off | Optional iroh direct-path — see [connectivity.md](connectivity.md). |
| `workload` | object | — | Connector knobs (below). |

**`AgentService`** (HTTPS): `hostname`, `origin` (host:port), `edgeTLSMode` (edge TLS behavior: `passthrough` default | `terminate`), `proxyProtocol` (string passed to the agent; e.g. `none` to disable the PROXY-protocol header; empty = agent default).

**`AgentL4Service`** (tcp/udp): `name` (unique within the list), `origin` (internal host:port), `preferredPort` (optional — pin the *public* listen port; honored if free), `hostname` (optional — intended for the future DNS handoff; currently informational).

**`workload`**: `replicas` (default `1`), `image` (default: the operator's built-in `codeberg.org/towonel/towonel-agent` tag — override to pick your own), `resources` (OOM-safe memory floor/ceiling applied if unset), `nodeSelector`, `tolerations`.

## Status (operator-written)

| Field | Description |
|---|---|
| `phase` | `Pending` → `WaitingForTunnel` → `Ready`. |
| `observedConfigHash` | Rollout trigger; changes when rendered env/SA/connectivity changes. |
| `renderedSecretRef` | The per-agent token Secret written in this namespace. |
| `observedGeneration` | Last reconciled generation. |

## Conditions

| Type | Meaning |
|---|---|
| `TunnelReady` | The referenced tunnel's token Secret is present and current. |
| `ConfigRendered` | The Secret + Deployment are rendered. |
| `PortsAllocated` | All declared tcp/udp services have a reserved public port (or none declared). |
| `WorkloadAvailable` | The agent Deployment reports `Available`. |
| `IrohConnectivityReady` | Direct-path connectivity applied. **Does not gate `Ready`** — absent when `connectivity` is unset (see [connectivity.md](connectivity.md)). |

## Behavior notes

- **Partial render.** HTTPS services render immediately; tcp/udp entries render as their port
  reservations appear on the tunnel, so an agent reaches `Ready` and re-renders as allocations land.
- **Rollout on change.** The agent reads everything from env; a routing/connectivity/image change
  bumps the config hash and rolls the Deployment.
- **No finalizer.** The agent has no hub-side state of its own; its children are garbage-collected by
  owner reference, and the tunnel re-aggregates via a watch when the agent is deleted.
- **Failover, not sharding.** Agents on one tunnel must serve the *same* hostname set (see
  [architecture.md](architecture.md#failover-not-sharding)); use `workload.replicas` or per-cluster
  agents for redundancy, not divergent hostname subsets.
