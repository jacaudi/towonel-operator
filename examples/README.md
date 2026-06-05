# Example custom resources

Mock CRs that exercise the design (`docs/designs/2026-06-04-towonel-operator-design.md`).
Field names are illustrative until the CRD schemas are implemented (P2+).

| File | What it shows |
|---|---|
| `01-tunnel.yaml` | `TowonelTunnel` — control plane (invite/token, hostnames, ports, DNS mode) + sample status |
| `02-agent.yaml` | `TowonelAgent` — workload + routing (HTTPS service, TCP with public hostname, connectivity, workload knobs) |
| `03-agent-failover.yaml` | A second agent on the **same** tunnel — N-agents-one-tunnel failover |
| `04-source-service-simple.yaml` | Source layer: annotated `Service`, single HTTPS exposure |
| `05-source-service-multiport.yaml` | Source layer: HTTP + raw TCP on one `Service` via port-name-scoped keys |
| `06-source-gateway-httproute.yaml` | Source layer: Gateway API (`Gateway` opt-in + `HTTPRoute`) |
| `07-emitted-dnsendpoint.yaml` | **Operator output** — the vendor-neutral external-dns `DNSEndpoint` it emits |

The two primitives you author are `TowonelTunnel` + `TowonelAgent` (01–03). The source
objects (04–06) are the dogfooding path — annotate and the operator materializes the
agent entries. `07` is produced by the operator, not authored.
