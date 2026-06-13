# Installation

## Prerequisites

- A Kubernetes cluster (the operator targets controller-runtime / modern K8s; envtest runs against 1.33).
- **A Towonel account + a personal API key** (`twk_…`). The `TowonelTunnel` controller calls
  the Towonel user API (`https://console.towonel.dev` by default) to create invites, converge
  authorized hostnames, and reserve ports. A personal key is sufficient for the core path
  (least privilege) — the operator-key/hub-admin tier is not required.
- **The Towonel agent image** — `codeberg.org/towonel/towonel-agent` (the operator *deploys*
  it; it does not build it). The default tag is set by the operator; override per agent with
  `spec.workload.image`.
- **Optional: Gateway API CRDs** — only if you want the `Gateway`/`HTTPRoute` source layer.
  The operator probes for them and gates those controllers automatically (see `gatewayAPI` below).
- **Optional: cert-manager / your own DNS** — the operator does not manage DNS (see [dns.md](dns.md)).

## Install with Helm (OCI)

The chart is published to GHCR as an OCI artifact.

```sh
helm install towonel-operator \
  oci://ghcr.io/jacaudi/charts/towonel-operator \
  --version <version> \
  --namespace towonel-system --create-namespace
```

Replace `<version>` with the chart version you want to install — use the
[latest release](https://github.com/jacaudi/towonel-operator/releases) (currently `v1.0.1`).

The image is `ghcr.io/jacaudi/towonel-operator` (multi-arch `linux/amd64` + `linux/arm64`);
the chart's `appVersion` selects a matching image tag, so you normally don't set `image.tag`.

### Provide the Towonel API key

The tunnel resolves its key from `spec.apiKeySecretRef`, falling back to a `TOWONEL_API_KEY`
env wired by the chart from a pre-existing Secret:

```sh
kubectl create secret generic towonel-api \
  --namespace towonel-system \
  --from-literal=token='twk_xxx'

helm upgrade --install towonel-operator \
  oci://ghcr.io/jacaudi/charts/towonel-operator --version <version> \
  --namespace towonel-system --create-namespace \
  --set credentials.existingSecret=towonel-api \
  --set credentials.tokenKey=token
```

Per-tunnel keys (via `TowonelTunnel.spec.apiKeySecretRef`) take precedence over this default and
are recommended when tunnels span teams/accounts.

## Chart values

| Value | Default | Purpose |
|---|---|---|
| `image.repository` | `ghcr.io/jacaudi/towonel-operator` | Operator image |
| `image.tag` | `""` (→ `.Chart.AppVersion`) | Pin an image tag |
| `replicas` | `1` | Manager replicas (HA via leader election) |
| `logLevel` | `info` | `debug\|info\|warn\|error` |
| `leaderElection.enabled` | `true` | Leader election for passive standby |
| `crds.install` | `true` | Render the CRD templates |
| `crds.keep` | `true` | `helm.sh/resource-policy: keep` so CRDs survive uninstall |
| `rbac.enabled` | `true` | Render the operator ClusterRole/Binding |
| `serviceAccount.create` | `true` | Create the operator ServiceAccount |
| `towonel.apiURL` | `https://console.towonel.dev` | Towonel hub base URL |
| `credentials.existingSecret` | `""` | Secret holding the API key (`TOWONEL_API_KEY` fallback) |
| `credentials.tokenKey` | `token` | Key within that Secret |
| `gatewayAPI.enabled` | `auto` | `auto` probes for Gateway API CRDs; `true`/`false` force the source controllers |
| `agentNamespace` | `""` | Namespace for auto-created default agents (`""` = the tunnel's namespace) |
| `agentNodeRBAC.create` | `true` | Shared node-reader `ClusterRole`/`ClusterRoleBinding` for [direct-path autodiscover](connectivity.md) (inert until used) |
| `resources` | 100m/128Mi → 500m/512Mi | Operator resource requests/limits |
| `observability.metrics.serviceMonitor.enabled` | `false` | Emit a Prometheus `ServiceMonitor` |

## Manager flags

The chart wires these; set them directly if you run the binary yourself:

| Flag | Default | Purpose |
|---|---|---|
| `--towonel-api-url` | `https://console.towonel.dev` | Towonel hub base URL |
| `--agent-namespace` | `""` | Namespace for auto-created default agents |
| `--enable-gateway-api` | `auto` | `auto\|true\|false` — register Gateway/HTTPRoute source controllers |
| `--leader-elect` | `true` | Leader election |
| `--metrics-bind-address` | `:8080` | Metrics endpoint |
| `--health-probe-bind-address` | `:8081` | Health/ready probes |
| `--version` | — | Print version and exit |

## Uninstall

```sh
helm uninstall towonel-operator -n towonel-system
```

CRDs are stamped `helm.sh/resource-policy: keep` (`crds.keep`), so they (and your tunnels/agents)
survive an uninstall. Delete them explicitly if you really want them gone. If you enabled
[direct-path autodiscover](connectivity.md), the shared node-reader `ClusterRole`/`ClusterRoleBinding`
are chart-owned and removed on uninstall.
