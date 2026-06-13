# DNS

> **Status: designed, not yet implemented.** The `TowonelTunnel.spec.dns` field exists in the CRD
> and validates, but the operator does **not** act on it — no `DNSEndpoint` resources are emitted and
> `status.dnsRecords` / `status.httpsEdgeTarget` are not populated. This page describes the intended
> design so the field's meaning is clear; do not rely on automated DNS yet.

## Today: point DNS yourself

Towonel has no DNS API. The tunnel exposes the information you need in its status:

- **HTTPS/SNI hostnames** → point a `CNAME` (or `A`) at your region's edge target.
- **Raw TCP/UDP** → the reservation's edge address is in `status.portAllocations[].edge.addresses`
  (and `status.edges`). Create an `A`/`AAAA` record to it; clients connect on `host:listenPort`
  (the port is not expressible in DNS).

So: read the tunnel status, create the records in whatever DNS you run.

## Planned: vendor-neutral handoff via external-dns `DNSEndpoint`

The design (parent design §8) hands DNS off through the ecosystem-standard external-dns
**`DNSEndpoint`** CRD (`externaldns.k8s.io/v1alpha1`) rather than talking to any DNS provider
directly. When `spec.dns.mode: DNSEndpoint`, the operator would:

- emit owner-reffed `DNSEndpoint` resources (GC'd with the tunnel) in `spec.dns.namespace`
  (default: the tunnel's namespace), with `recordTTL` from `spec.dns.defaultTTL`;
- compute endpoints from the authorized hostnames + reservations: HTTPS hostnames → `CNAME` to the
  configured regional edge target; tcp/udp entries that set a `hostname` → `A`/`AAAA` to the edge
  addresses;
- populate `status.dnsRecords` as an observability view (never an integration contract).

external-dns (run with `--source=crd`) then reconciles those to **any** provider (Cloudflare,
Route53, Google, RFC2136, webhook/NextDNS, …). The operator would not own the `DNSEndpoint` CRD —
you install it as part of your external-dns setup.

Why `DNSEndpoint` and not, say, a ConfigMap: external-dns has no ConfigMap source, and a
`DNSEndpoint` carries its records inline — it *is* the standard "declare records, let a controller
apply them" artifact. A bespoke shape would only be consumable by a bespoke controller, which the
vendor-neutral design avoids.

## Related fields (currently inert)

- `TowonelTunnel.spec.dns.{mode, defaultTTL, namespace}` — accepted, no effect yet.
- `TowonelTunnel.status.{dnsRecords, httpsEdgeTarget}` — reserved for the handoff.
- `TowonelAgent.spec.tcp[]/udp[].hostname` and the `towonel.io/dns-record` source annotation — the
  inputs the handoff would consume; today they are informational only.
