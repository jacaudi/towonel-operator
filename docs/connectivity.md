# Direct-path connectivity (`spec.connectivity`)

An **optional** optimization on [`TowonelAgent`](crd-towonelagent.md). It advertises a reachable
address for the agent so peers can connect over a **direct** iroh path instead of relaying. Agents
work fine without it — the baseline (agent dials out, relay + hole-punching) needs no inbound ports.

## Outbound vs inbound

| Flow | Direction | Inbound port? |
|---|---|---|
| Agent → edge (control/relay, always) | Outbound | No |
| Edge/peer → agent **direct** path (this feature) | **Inbound** | **Yes** |

So `spec.connectivity` is the opt-in case where the agent's node **is** externally reachable (public
IP, or a NAT port-forward) and you want to advertise a stable inbound UDP address. If the node isn't
actually reachable, it simply no-ops and traffic stays on the relay.

## Spec

```yaml
spec:
  connectivity:
    autodiscover: false          # advertise node IP + UDP NodePort via the agent's K8s autodiscover
    irohPort: 0                  # pin the agent's bound UDP port (required when nodePort.create)
    extraLocalAddrs: []          # manual reachable socket addrs (CSV; IPv6 in brackets)
    nodePort:
      create: false              # operator creates+owns a UDP NodePort Service
      name: ""                   # optional Service name override (default <agent>-iroh)
      port: 0                    # optional pinned external node port (30000-32767; else auto)
```

The two simple knobs are independent and dependency-free:

- **`extraLocalAddrs`** — the trivial path. If you already know the node's public `ip:port`, set it;
  no RBAC, no Service, no autodiscover. Renders `TOWONEL_AGENT_EXTRA_LOCAL_ADDRS`.
- **`irohPort`** — pins the agent's bound UDP port (`TOWONEL_AGENT_IROH_PORT`) + a UDP container port.

## Autodiscover (the managed path)

When `autodiscover: true`, the agent (running in its pod) reads its **Node** (by `NODE_NAME`) and a
**UDP NodePort Service** to advertise `node_ip:nodePort` as an iroh address. The operator wires this:

- env `TOWONEL_AGENT_K8S_AUTODISCOVER` + `_K8S_SERVICE` + `_K8S_NAMESPACE`, and `NODE_NAME` via the
  downward API (`fieldRef: spec.nodeName`);
- a per-agent **ServiceAccount** (always set on the pod, even when connectivity is off);
- a per-agent **UDP NodePort Service** (`externalTrafficPolicy: Local`, so `nodeX:port` routes to the
  pod on `nodeX` and preserves the source IP) + a per-agent services-reader `Role`/`RoleBinding`;
- a **shared, chart-owned** node-reader `ClusterRole`/`ClusterRoleBinding`
  (`towonel-operator-agent-node-reader`, enabled by `agentNodeRBAC.create`) whose subjects the
  operator patches with each autodiscover agent's ServiceAccount.

### Validation matrix (never wedges)

`autodiscover` is the master switch. Invalid combinations emit an `InvalidConfig` Event and **skip**
the connectivity setup — the agent still reconciles to `Ready` and runs via relay.

| Config | Result |
|---|---|
| `extraLocalAddrs` and/or `irohPort` only | Env set. No Service/RBAC. |
| `autodiscover: true` + `nodePort.create: true` + `irohPort > 0` | Full autodiscover path applied. |
| `autodiscover: true` without `nodePort.create` or `irohPort` | Event + skip (agent still Ready). |
| `nodePort.create: true` without `autodiscover` | Event + skip (no dead Service). |
| `nodePort.port` set without `nodePort.create` | Ignored (informational Event). |

## Status

The `IrohConnectivityReady` condition reports the direct-path state (`True` applied, `False` with a
reason when skipped or the chart node-RBAC shell is missing, absent when unrequested). It **does not
gate** the agent's `Ready` phase — connectivity is optional.

## Notes

- `agentNodeRBAC.create` (chart, default on) ships the shared node-reader shell; it's inert until an
  autodiscover agent adds its ServiceAccount as a subject. Disable it on strict-RBAC clusters
  (autodiscover then surfaces a `NodeRBACShellMissing` Event and degrades to relay).
- With `replicas > 1`, each pod advertises its own node; `externalTrafficPolicy: Local` keeps the
  per-node advertisements coherent.
