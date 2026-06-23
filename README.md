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

## What it does

Towonel exposes services behind NAT/CGNAT without inbound ports. This operator drives
that from Kubernetes via two resources:

- **`TowonelTunnel`** — the control plane. Reconciles a Towonel invite (token,
  authorized hostnames, TCP/UDP port reservations) against the Towonel API and owns
  the token Secret.
- **`TowonelAgent`** — a placement-specific workload that references a tunnel and
  renders the agent Deployment plus its `hostname → origin` routing. Multiple agents
  can share one tunnel for failover.

You can author those resources directly, or annotate a `Service`, `Gateway`, or
`HTTPRoute` and let the operator materialize them (the dogfooding path). Set
`towonel.io/auto-routes: "true"` on a Gateway to tunnel its same-namespace HTTPRoutes
without annotating each one — see [Gateway auto-routes](docs/source-layer.md#gateway-auto-routes-towonelioauto-routes).
A Gateway can extend auto-routes to other namespaces with an opt-in `towonel.io/auto-routes-namespaces` allowlist (off by default) — note the [security implications](docs/source-layer.md#cross-namespace-auto-routes-towonelioauto-routes-namespaces) of public cross-namespace exposure.
See [`docs/architecture.md`](docs/architecture.md) for the why behind the two-resource split.

## Quickstart

**Prerequisites:** a Towonel account + a personal API key, and a Kubernetes
cluster. (Full prerequisites and chart values: [`docs/installation.md`](docs/installation.md).)

**1. Install the operator** (Helm, OCI chart on GHCR):

```sh
kubectl create namespace towonel-system
kubectl create secret generic towonel-api -n towonel-system --from-literal=token='twk_xxx'

helm install towonel-operator \
  oci://ghcr.io/jacaudi/charts/towonel-operator --version <version> \
  --namespace towonel-system \
  --set credentials.existingSecret=towonel-api
```

Replace `<version>` with the chart version you want — use the [latest release](https://github.com/jacaudi/towonel-operator/releases) (currently `v1.0.1`).

**2. Create a tunnel** (the control plane — provisions the Towonel invite + token):

```yaml
apiVersion: towonel.io/v1alpha1
kind: TowonelTunnel
metadata:
  name: app
  namespace: towonel-system
spec:
  region: EU
  apiKeySecretRef:
    name: towonel-api
    key: token
  deletionPolicy: Retain
```

**3. Expose a service** — either annotate it:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: grafana
  namespace: monitoring
  annotations:
    towonel.io/tunnel: enable
    towonel.io/tunnel-ref: towonel-system/app
    towonel.io/hostname: grafana.example.com
spec:
  selector:
    app.kubernetes.io/name: grafana
  ports:
    - name: http
      port: 3000
      targetPort: 3000
```

…or author a `TowonelAgent` directly for full control (HTTPS + raw TCP/UDP, failover,
[direct-path connectivity](docs/connectivity.md)) — see [`docs/examples/`](docs/examples/).

**4. Point DNS.** The operator does not manage DNS yet — read the tunnel's status
(`authorizedHostnames`, `portAllocations[].edge.addresses`) and create the records in your
DNS provider. See [`docs/dns.md`](docs/dns.md).

```sh
kubectl get towoneltunnel app -n towonel-system -o yaml   # inspect status
```

## Documentation

Full docs live in [`docs/`](docs/):

- [Installation](docs/installation.md) · [Architecture](docs/architecture.md)
- [`TowonelTunnel`](docs/crd-towoneltunnel.md) · [`TowonelAgent`](docs/crd-towonelagent.md) references
- [Source layer (annotations)](docs/source-layer.md) · [Direct-path connectivity](docs/connectivity.md) · [DNS](docs/dns.md)
- [Examples](docs/examples/)

## Acknowledgements

- **[Bernd Schorgers (bjw-s)](https://github.com/bjw-s)** — this operator's Helm chart is built on
  the [bjw-s `app-template`](https://github.com/bjw-s-labs/helm-charts) common library, which does the
  heavy lifting of rendering the controller Deployment, RBAC, service account, and service. Thank you
  for an excellent, reusable chart!
- **[Erwan Leboucher (eleboucher)](https://github.com/eleboucher)** — the creator of
  [Towonel](https://towonel.dev) itself ([codeberg](https://codeberg.org/towonel/towonel) ·
  [github](https://github.com/eleboucher/towonel)). This operator only automates the client side;
  Towonel — the hub, edges, and agent — is his work. Thank you for creating a self-hosted
  Cloudflare Tunnel replacement!

## License

MIT
