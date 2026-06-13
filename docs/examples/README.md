# Example custom resources

Sample manifests for the two primitives and the annotation source layer. Pair them
with the reference docs: [TowonelTunnel](../crd-towoneltunnel.md),
[TowonelAgent](../crd-towonelagent.md), [source layer](../source-layer.md).

| File | What it shows |
|---|---|
| [`01-tunnel.yaml`](01-tunnel.yaml) | `TowonelTunnel` — control plane (invite/token, hostnames, ports) + sample reconciled status |
| [`02-agent.yaml`](02-agent.yaml) | `TowonelAgent` — workload + routing (HTTPS service, TCP with a public hostname, connectivity, workload knobs) |
| [`03-agent-failover.yaml`](03-agent-failover.yaml) | A second agent on the **same** tunnel — N-agents-one-tunnel failover |
| [`04-source-service-simple.yaml`](04-source-service-simple.yaml) | Source layer: annotated `Service`, single HTTPS exposure |
| [`05-source-service-multiport.yaml`](05-source-service-multiport.yaml) | Source layer: HTTP + raw TCP on one `Service` via port-name-scoped keys |
| [`06-source-gateway-httproute.yaml`](06-source-gateway-httproute.yaml) | Source layer: Gateway API (`Gateway` opt-in + `HTTPRoute`) |

The two primitives you author are `TowonelTunnel` + `TowonelAgent` (01–03). The source
objects (04–06) are the dogfooding path — annotate a `Service`/`Gateway`/`HTTPRoute` and
the operator materializes and owns the agent's routing entries, exactly as if you had
hand-authored them.

> **DNS:** these examples expose services and reserve ports, but the operator does **not**
> create public DNS records — DNS handoff (`spec.dns`) is designed but not yet implemented
> (see [../dns.md](../dns.md)). Point your DNS at the edge addresses in the tunnel's status
> yourself for now.
