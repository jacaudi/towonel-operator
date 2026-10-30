# DNS

With `spec.dns.mode: DNSEndpoint`, the operator publishes public DNS for every HTTPS hostname
the hub has confirmed for the tunnel. It does not call any DNS provider. Instead it emits one
external-dns [`DNSEndpoint`](https://kubernetes-sigs.github.io/external-dns/latest/docs/sources/crd/)
(`externaldns.k8s.io/v1alpha1`) per tunnel. Any `DNSEndpoint`-aware controller publishes it;
external-dns with `--source=crd` is the reference.

```yaml
spec:
  dns:
    mode: DNSEndpoint                # default Disabled — enable only after the rollout order below
    labels:                          # copied onto the DNSEndpoint — scope external-dns with them
      example.com/dns-endpoint: public
    providerSpecific:                # copied onto every endpoint
      - name: cloudflare/proxied
        value: "false"
    # defaultTTL: 300                # record TTL (seconds, >= 1)
    # cnameTarget: edge.example.com  # override the edge target for this tunnel
    # includeWildcards: false        # also publish *.zone hostnames
    # namespace: external-dns        # where the DNSEndpoint lives (default: the tunnel's)
    # annotations: {}                # copied onto the DNSEndpoint (--annotation-filter)
```

## What gets published

- One `CNAME <hostname> → <edge target>` for every hostname in the invite that the hub has
  **confirmed**. The record set follows the hub, not just your spec:
  - A hostname the hub refused with `hostname_conflict` stays out: another tenant may hold it.
  - A **revoked** invite publishes nothing.
  - Wildcard hostnames (`*.example.com`) are skipped unless `includeWildcards: true`.
- **Edge target.** The target is `spec.dns.cnameTarget` if set. Otherwise it comes from the
  operator's region map, keyed on the **hub's** invite region (not `spec.region`, which only
  matters when the invite is created):
  - On the public hub (`towonel.apiURL` / `--towonel-api-url` = `https://hub.towonel.dev`) the
    defaults are `CA → edge-na.towonel.dev` and `EU → edge-eu.towonel.dev`.
  - For a self-hosted hub, set Helm `dns.edgeTargets` (`--dns-edge-target REGION=FQDN`).
    Without it, the tunnel reports `NoEdgeTarget`.
  - Explicit `dns.edgeTargets` / `--dns-edge-target` entries **replace** the public-hub defaults
    entirely, so set every region you use.
- **The object.** It is named `towonel.<tunnel-namespace>.<tunnel-name>`. If that would exceed the
  253-character name limit, it is named `towonel.<hash>` instead, where `<hash>` is the same value as
  the `towonel.io/owner-tunnel` label. It lives in `spec.dns.namespace` (default: the tunnel's namespace).
  - It is labelled `app.kubernetes.io/managed-by: towonel-operator` and
    `towonel.io/owner-tunnel: <hash>`, and annotated `towonel.io/owner-tunnel-ref: <ns>/<name>`.
  - It has no owner reference. The operator cleans it up by label.
- `status.dnsRecords` shows the records (also in `Disabled` mode, so you can create them by hand).
  `status.regionEndpoints` shows the live edge IPs per region.
- The `DNSEndpointSynced` condition reports progress:

  | Reason | Meaning |
  |---|---|
  | `Synced` | Records published, or 0 records and the object removed |
  | `DNSEndpointCRDMissing` | Install external-dns's CRD; it is picked up without a restart |
  | `NoEdgeTarget` | No target for the invite's region; records withdrawn |
  | `InviteRevoked` | The hub revoked the invite, or no longer knows it (deleted hub-side); records withdrawn |
  | `ForeignObject` | An object the operator doesn't own holds the name; nothing is overwritten |
  | `InvalidConfig` | The API server rejected the object (for example a bad label key) |
  | `APIError` | An API error, transient or persistent (for example `Forbidden` or a missing `spec.dns.namespace`); retried every 2 minutes |

  In `Disabled` mode the condition is removed once the sweep succeeds. If the sweep fails it is
  `False/APIError` and retried. DNS never affects the tunnel's `Ready`.

## Consumer setup

external-dns `--label-filter` and `--annotation-filter` apply to **every** source on an instance
(crd, ingress, gateway routes, service). With `policy: sync`, filtering a source out **deletes**
its records. A `!=` label selector matches objects that lack the label.

**Recipe A: dedicated public instance (recommended).**

1. Run an external-dns instance for your public provider with:
   - `sources: [crd]`;
   - `domainFilters: [<your zone>]`;
   - `--label-filter=<you>/dns-endpoint=public`;
   - its own `txtOwnerId`.
2. On every other instance that reads `crd` for the same zone (for example a LAN split-horizon
   one), add `--label-filter=<you>/dns-endpoint!=public`.
3. Label the tunnel: `spec.dns.labels: {<you>/dns-endpoint: public}`. Use a label prefix you
   own, not `towonel.io/`.

**Recipe B: reuse an existing public instance.** Adding your zone to its `domainFilters`
publishes **every** source it reads in that zone: ingress, gateway routes and services, possibly
with LAN IPs. It also publishes LAN-only `DNSEndpoint`s. Each of those must be excluded on that
instance. Prefer recipe A.

**Rollout order (mandatory):**

1. The LAN instance's `!=public` filter.
2. The public instance.
3. `mode: DNSEndpoint`.

Otherwise a LAN instance with `crd` and `policy: sync` writes the public CNAMEs over its local
records.

**Cloudflare proxying.** `providerSpecific: [{name: cloudflare/proxied, value: "false"}]`
publishes DNS-only records, overriding an instance-wide `--cloudflare-proxied`. Property names
by external-dns version:

- `cloudflare/proxied` from v0.23.0;
- `external-dns.kubernetes.io/cloudflare-proxied` in v0.22.0;
- `external-dns.alpha.kubernetes.io/cloudflare-proxied` in v0.21.0 and earlier.

## Upgrading

> **Warning.** Before this release `spec.dns.mode: DNSEndpoint` was accepted but inert. A tunnel
> that already sets it **starts emitting on upgrade**, bypassing the rollout order above. Find
> such tunnels first:
>
> ```sh
> kubectl get twt -A -o jsonpath='{range .items[?(@.spec.dns.mode=="DNSEndpoint")]}{.metadata.namespace}/{.metadata.name}{"\n"}{end}'
> ```
>
> Set each one to `mode: Disabled` before upgrading, or complete the rollout order above first.

If you install CRDs from raw manifests or set `crds.install: false`, upgrade the TowonelTunnel CRD before enabling `mode: DNSEndpoint`; with a stale CRD, `spec.dns.labels` is unknown — strict clients reject it, non-strict clients silently prune it, emitting an unlabelled DNSEndpoint that your LAN instance would pick up.

**Changes every tunnel sees after upgrading, even with DNS left `Disabled`:**

- `status.dnsRecords` is populated with the records the tunnel *would* publish, whenever an edge
  target resolves for its region.
- A `RegionEndpointsAvailable` condition and `status.regionEndpoints` (live edge IPs per region)
  appear.
- The operator lists `DNSEndpoint`s once per reconcile (a no-op `NoMatch` when the CRD isn't
  installed). This needs the new RBAC rule (`externaldns.k8s.io` / `dnsendpoints`:
  `get, list, create, patch, delete`). The Helm chart grants it. **If you install from raw
  manifests, update the operator's ClusterRole** from `config/rbac/role.yaml`, or DNS operations
  fail with `Forbidden`. Under `deletionPolicy: Delete`, a `Forbidden` sweep keeps the tunnel's
  finalizer until RBAC is fixed.

**Migrating from hand-made records.** external-dns won't modify records it doesn't own (no TXT
ownership record), so remove the existing records first. If another controller manages them (for
example cloudflare-operator `CloudflareDNSRecord` CRs), remove those CRs, then check at the
provider that the records are gone. Then enable. Expect a resolution gap of about one TTL.

## Removal and cleanup

- Under `policy: sync`, public DNS is **removed** when:
  - the tunnel switches to `Disabled`;
  - the hub revokes or deletes the invite;
  - the edge target disappears;
  - the tunnel is deleted with `deletionPolicy: Delete` (records are withdrawn before the
    invite's hostnames are released).
- `deletionPolicy: Retain` keeps the records, and from then on they are **unmanaged**: nothing
  updates or removes them.
  - A tunnel recreated with the same namespace/name re-adopts them only once its token Secret
    (`<tunnel>-token`) is present again. Garbage collection deletes that Secret along with the old
    tunnel, so until you restore it the recreated tunnel stops at `Ready=False` ("adopted invite
    has no stored token") before DNS runs.
  - Otherwise remove the records with the orphan-cleanup command below.
  - A re-adopting tunnel recreated with `Disabled` removes them.
- A removed hostname lingers for one external-dns sync interval plus the TTL. If hostname
  convergence fails (other than the hub reporting the invite deleted, which withdraws the records), the last
  published set stays until the next success.
- Orphans (for example after a force-removed finalizer):
  `kubectl delete dnsendpoints -A -l towonel.io/owner-tunnel=<hash>`. The
  `towonel.io/owner-tunnel-ref` annotation shows which tunnel an object belonged to.
  Find the hash with
  `kubectl get dnsendpoints -A -l app.kubernetes.io/managed-by=towonel-operator -L towonel.io/owner-tunnel`.
- `spec.dns.namespace` lets a tunnel author place the `DNSEndpoint` in any existing namespace,
  through the operator's ClusterRole.

## Raw TCP/UDP

Not automated yet. The reservation's edge address is in `status.portAllocations[].edge.addresses`.
Create an `A`/`AAAA` record yourself. Clients connect on `host:listenPort`; the port can't be
expressed in DNS.

## SNI

A CNAME chain doesn't change SNI, so Towonel still routes on the original hostname.
