# `TowonelTunnel` reference

The control-plane primitive. Reconciles a Towonel invite (token, authorized hostnames, TCP/UDP
port reservations) against the Towonel user API and owns the token Secret. **Namespaced**
(`shortName: twt`). It derives its authorized-hostname set and port reservations by aggregating
the [`TowonelAgent`](crd-towonelagent.md)s that reference it.

See [`examples/01-tunnel.yaml`](examples/01-tunnel.yaml).

## Spec

| Field | Type | Default | Description |
|---|---|---|---|
| `region` | string | `EU` | Primary region (`POST /v1/invites .region`). Supported: `EU`, `CA`. |
| `failoverRegions` | []string | — | Extra regions whose edges agents also dial (`EU`, `CA`). |
| `apiKeySecretRef` | `{name, key}` | — | Secret holding the Towonel API key. Omitted → the operator's `TOWONEL_API_KEY` default (chart `credentials.existingSecret`). A personal `twk_` key suffices. |
| `tokenExpiry` | int64 | `0` | Invite `expires_in_secs`; `0` = never (the hub caps finite values at 30d). |
| `deletionPolicy` | `Delete`\|`Retain` | `Delete` | On CR delete: `Delete` releases the invite + ports; `Retain` leaves the invite alive (prod tunnels). |
| `extraHostnames` | []string | — | Authorize hostnames no agent serves yet (escape hatch). |
| `dns` | object | `{mode: Disabled}` | **Reserved / not yet implemented** — see [dns.md](dns.md). The field is accepted but the operator emits nothing. |

## Status (operator-written)

| Field | Description |
|---|---|
| `inviteId` / `tenantId` | The created invite + tenant (ports are tenant-scoped). |
| `tokenSecretRef` | `{name, namespace}` of the token Secret the tunnel wrote. |
| `expiresAt` | Token expiry in epoch ms; `0` = never. |
| `authorizedHostnames` | Aggregated from referencing agents + `extraHostnames`. |
| `portAllocations` | One per agent tcp/udp service: `{name, protocol, listenPort, edge{nodeId, addresses}}`. |
| `edges` | Union of edge addresses (TCP/UDP). |
| `httpsEdgeTarget` / `dnsRecords` | **Reserved** — populated only by the (unimplemented) DNS handoff. |
| `phase` | Coarse rollup: `Pending` / `Ready` / `Error`. |
| `observedGeneration` | Last reconciled generation. |

## Conditions

| Type | Meaning |
|---|---|
| `Ready` | Invite issued, token stored, hostnames + ports converged. |
| `InviteIssued` | The Towonel invite exists and the token Secret is written. |
| `HostnamesSynced` | The authorized-hostname set matches the desired (aggregated) set. |
| `PortsReserved` | All requested TCP/UDP ports are reserved. |
| `TokenExpiringSoon` | Set when a finite token nears expiry (7-day window). |

## Lifecycle notes

- **`extraHostnames` semantics.** This is an additive allow-list of authorized SNI hostnames — it
  is *unioned* with the hostnames contributed by referencing agents' HTTPS services, not a
  replacement for them. It is not the only source of authorized hostnames; use it only to authorize
  names no agent serves yet.
- **Token Secret.** The tunnel writes the canonical token Secret in its own namespace; each agent
  copies it into the agent's namespace. The API key never enters agent pods.
- **GC / finalizer.** A finalizer releases the invite (`DELETE /v1/invites/{id}`) and reserved ports
  on delete, honoring `deletionPolicy: Retain`.
- **Token rotation.** Default `tokenExpiry: 0` avoids rotation churn. There is no token-refresh
  endpoint upstream; finite-expiry auto-rotation is designed but not yet implemented.
- **Multi-cluster.** Each cluster runs its own operator + tunnel CR referencing the *same* invite
  (shared hostnames = Towonel's native failover). Keep the aggregated hostname/port set identical
  across clusters (see [architecture.md](architecture.md#failover-not-sharding)).
