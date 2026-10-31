# DNS

*Optional, off by default.*

With `spec.dns.mode: DNSEndpoint`, the operator publishes public DNS for every HTTPS hostname
the hub has confirmed for the tunnel. It does not call any DNS provider. Instead it emits one
external-dns [`DNSEndpoint`](https://kubernetes-sigs.github.io/external-dns/latest/docs/sources/crd/)
(`externaldns.k8s.io/v1alpha1`) per tunnel. Any `DNSEndpoint`-aware controller publishes it;
external-dns with `--source=crd` is the reference. The fields below live on
[`TowonelTunnel`](crd-towoneltunnel.md); the install side is in [installation.md](installation.md).

![Hostnames the Towonel hub confirms for an invite are turned by towonel-operator into a DNSEndpoint that CNAMEs each one to its region's edge; external-dns instances filtered by label then publish it to a public provider while a LAN instance ignores it.](images/02-dns-handoff.svg)

*The operator declares the records; whatever reads `DNSEndpoint` objects applies them.*

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
  - A hostname you **opted out** stays out (below).
- **Opt-out.** A hostname can be kept out of both the `DNSEndpoint` and `status.dnsRecords` while
  the hub still authorizes it, so Towonel keeps routing it on SNI. Use it when something else owns
  that name's DNS (see [Zone-apex hostnames](#zone-apex-hostnames)).
  - **On a source:** annotate the `Service`, `Gateway` or `HTTPRoute` with
    `towonel.io/dns-record: "false"` (falsy values as for `towonel.io/tunnel`: `false`, `no`,
    `disable`, `disabled`). It covers every hostname **that object** contributes: a Gateway's
    listener hostnames, or an HTTPRoute's own `spec.hostnames`. It is not inherited: a Gateway
    annotation does not reach the HTTPRoutes auto-routed under it, so annotate each route.
    Only an absent annotation or a truthy value (`true`, `yes`, `enable`, `enabled`) publishes. Any
    other value opts out, so a typo (`flase`) fails closed and the object gets an
    `InvalidAnnotationValue` Warning Event.
  - **On a hand-written agent:** set `spec.services[].dnsRecord: false`
    (see [crd-towonelagent.md](crd-towonelagent.md)).
  - **Any opt-out wins.** If several agents of the tunnel serve the same hostname and one of them
    opts out, the hostname is excluded. This fails closed: you never get a public record for a
    name you excluded. The same holds across sources: if a Gateway listener hostname and an
    HTTPRoute hostname are identical and both target the same agent, they share one `services[]`
    entry there, so opting out either object opts the name out for both. Even when they target
    different agents, the tunnel-level rule above excludes the name.
  - **`spec.extraHostnames` have no opt-out of their own**, because no agent serves them. A name
    that is in `extraHostnames` and also opted out on an agent is excluded.
  - Opting out every hostname leaves zero records, so the `DNSEndpoint` is removed
    (`Synced`, "0 records").
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
- `status.dnsRecords` shows the records (also in `Disabled` mode, so you can create them by hand);
  hostnames you opted out are omitted. `status.regionEndpoints` shows the live edge IPs per region.
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

## Zone-apex hostnames

A hub-confirmed hostname that is the zone apex itself (`example.com`, not `www.example.com`) gets
a `CNAME` at the apex like any other name. Whether that works depends on your DNS provider:

- DNS does not allow a `CNAME` next to the other records an apex must carry (`SOA`, `NS`), so many
  providers reject an apex `CNAME`, or accept it only as a proprietary `ALIAS`/`ANAME` type.
- Some providers flatten it instead. Cloudflare, for example, resolves an apex `CNAME` to
  `A`/`AAAA` answers.
- The operator cannot see your provider, and this page does not list per-provider behaviour:
  check yours before publishing an apex name.

If your provider rejects it, opt the apex hostname out and manage its record yourself:

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: apex
  annotations:
    towonel.io/tunnel: "true"
    towonel.io/dns-record: "false"   # authorized at the hub, but no CNAME is published for it
spec:
  hostnames: ["example.com"]
  # parentRefs, rules ...
```

Then create the record by hand:

- **`ALIAS`/`ANAME`**, where your provider has one: point it at the tunnel's edge target, which is
  `spec.dns.cnameTarget` if set, else your region's edge (see [Edge target](#what-gets-published)).
  The apex is opted out, so it never appears in `status.dnsRecords`. Without
  `spec.dns.cnameTarget`, the target is your region's edge from the edge-target map: on the public
  hub `CA` → `edge-na.towonel.dev` and `EU` → `edge-eu.towonel.dev`, or what you set with
  `--dns-edge-target`. A sibling hostname's record in `status.dnsRecords` shows the same target.
- **`A`/`AAAA`**: use the addresses of the `status.regionEndpoints` entry with `role: primary`.
  These do not follow the edge when its addresses change, so re-check them against
  `status.regionEndpoints` now and then. An `ALIAS`/`ANAME` follows the target.

## Consumer setup

external-dns `--label-filter` and `--annotation-filter` apply to **every** source on an instance
(crd, ingress, gateway routes, service). With `policy: sync`, filtering a source out **deletes**
its records. A `!=` label selector matches objects that lack the label.

Pick one of two recipes. Both put a label on the tunnel's `DNSEndpoint` through `spec.dns.labels`;
use a label prefix you own (`<you>/`), not `towonel.io/`.

- **Recipe A: dedicated public instance.** You want isolation: the instance reads only labelled
  `DNSEndpoint`s, so it cannot publish anything else by accident.
- **Recipe B: reuse your existing public and LAN instances.** You want fewer moving parts: no new
  instance, at the price of auditing what the public one will now publish.

### Recipe A: dedicated public instance

1. Run an external-dns instance for your public provider with:
   - `sources: [crd]`;
   - `domainFilters: [<your zone>]`;
   - `--label-filter=<you>/dns-endpoint=public`;
   - its own `txtOwnerId`.
2. On every other instance that reads `crd` for the same zone (for example a LAN split-horizon
   one), add `--label-filter=<you>/dns-endpoint!=public`.
3. Label the tunnel: `spec.dns.labels: {<you>/dns-endpoint: public}`.

### Recipe B: reuse your existing public and LAN instances

You already run a LAN external-dns instance and a public one. Adding the tunnel's zone to the
public instance's `domainFilters` makes it publish **every** source it reads in that zone:
ingresses, gateway routes, services and other `DNSEndpoint`s, possibly with LAN IPs. Label
what is LAN-only, and filter on both instances.

1. **Audit, then label.** Before you add the zone to the public instance, list everything that
   instance would now publish in that zone: other `DNSEndpoint`s, ingresses, gateway routes and
   services. Check its `--gateway-label-filter` and ingress class to see which of them it reads at
   all. Label every LAN-only object `<you>/dns-endpoint: lan`. The label filter applies to every
   source's objects **except Gateways themselves**, which only `--gateway-label-filter` filters.
   That includes **the routes and Services you tunnel**: without the label the public instance
   would publish their own records (your LAN gateway address) under the same names as the tunnel's
   CNAMEs; the tunnel's `DNSEndpoint` provides their public record. For a Gateway you tunnel
   through its listener hostnames, label the HTTPRoutes attached to those listeners `lan`
   (including routes auto-selected by `towonel.io/auto-routes`), or exclude that Gateway on the
   public instance with `--gateway-label-filter`. A public instance whose gateway or ingress
   filters already exclude those sources is safe either way, but labelling is the robust choice.
2. **LAN instance:** add `<you>/dns-endpoint!=public` to its `--label-filter`.
3. **Public instance:** add the tunnel's zone to `domainFilters` and add
   `<you>/dns-endpoint!=lan` to its `--label-filter`.
4. **Tunnel:** `spec.dns.labels: {<you>/dns-endpoint: public}`.

"Add" means comma-join with any selector the instance already has (`a=b,<you>/dns-endpoint!=lan`).
Replacing an existing selector can publish objects the old selector excluded, and under
`policy: sync` it deletes the records of objects the new selector drops.

| Object | Label `<you>/dns-endpoint` | LAN instance (`!=public`) | Public instance (`!=lan`) |
|---|---|---|---|
| This tunnel's `DNSEndpoint` | `public` | skips it | publishes it |
| LAN-only `DNSEndpoint` | `lan` | publishes it | skips it |
| Tunneled `HTTPRoute` or Service | `lan` | publishes it | skips it |
| Gateway tunneled via its listeners | label its attached routes `lan`, or exclude it with `--gateway-label-filter` | publishes it | skips it |
| Anything else unlabelled (ingress, route, service, other `DNSEndpoint`) | none | publishes it | publishes it |

Use `!=` on **both** sides because an object with no label still matches a `!=` selector, so
neither instance loses what it publishes today. Do not use `=public` on the **reused** public
instance: with `policy: sync` it would filter out the instance's other records and **delete**
them. (Recipe A's dedicated instance does use `=public`: it publishes nothing else.)

> **Why the audit matters:** the last table row. Whatever is unlabelled and in the tunnel's zone is now
> public too.

### Rollout order (mandatory)

1. The LAN-side filters:
   - Recipe A: the LAN instance's `!=public` filter.
   - Recipe B: audit, then label the LAN-only and tunneled objects `lan` (step 1), then add the
     LAN instance's `!=public` filter.
2. The public instance:
   - Recipe A: start it.
   - Recipe B: add the tunnel's zone to its `domainFilters` together with its `!=lan` filter.
3. `mode: DNSEndpoint`.

Otherwise a LAN instance with `crd` and `policy: sync` writes the public CNAMEs over its local
records.

### Cloudflare proxying

`providerSpecific: [{name: cloudflare/proxied, value: "false"}]`
publishes DNS-only records, overriding an instance-wide `--cloudflare-proxied`. Property names
by external-dns version:

- `cloudflare/proxied` from v0.23.0;
- `external-dns.kubernetes.io/cloudflare-proxied` in v0.22.0;
- `external-dns.alpha.kubernetes.io/cloudflare-proxied` in v0.21.0 and earlier.

### k8s_gateway on the LAN

If your LAN resolver is [k8s_gateway](https://codeberg.org/k8s-gateway/k8s_gateway) (the GitHub
repository is its read-only mirror) rather than external-dns, the `<you>/dns-endpoint` filters
above do not apply to it. Its README says:

- It watches `DNSEndpoint` objects only if `resources` in its Corefile lists `DNSEndpoint`. The
  default is `Ingress` and `Service`.
- The only documented exclusion for a `DNSEndpoint` is the label `k8s-gateway.dns/ignore: "true"`
  (other kinds also have `ingressClasses`, `gatewayClasses` and `serviceLabelSelectors`).

So if your `k8s_gateway` has `DNSEndpoint` in `resources`, add the label next to your own:

```yaml
spec:
  dns:
    labels:
      example.com/dns-endpoint: public   # for your external-dns instances
      k8s-gateway.dns/ignore: "true"     # keeps k8s_gateway from reading this DNSEndpoint
```

In its source, only `A`, `AAAA` and `TXT` endpoints produce answers (`dnsendpoint.go`), and
`getMatchingAddresses` (`gateway.go`) skips a lookup that yields neither addresses nor `TXT` data.
So a `DNSEndpoint` that holds only the operator's `CNAME`s cannot change an answer, and the
label is belt-and-braces.

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

If you install CRDs from raw manifests or set `crds.install: false`, upgrade the TowonelTunnel CRD before enabling `mode: DNSEndpoint`; with a stale CRD, `spec.dns.labels` is unknown — strict clients reject it, non-strict clients silently prune it, emitting an unlabelled DNSEndpoint that your LAN instance would pick up. Likewise upgrade the TowonelAgent CRD before relying on `towonel.io/dns-record` or `spec.services[].dnsRecord`: with a stale CRD, `dnsRecord` is unknown — strict clients reject it, non-strict clients silently prune it, so the name is published.

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
  - a hostname is opted out (just that name's record);
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

---

[← Back to README](../README.md)
