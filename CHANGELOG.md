# Changelog

## [1.6.0](https://github.com/jacaudi/towonel-operator/compare/v1.5.0...v1.6.0) (2026-06-24)
### Features

* expose operator-managed agent /metrics for Prometheus scraping ([#36](https://github.com/jacaudi/towonel-operator/issues/36)) ([8d8e9af](https://github.com/jacaudi/towonel-operator/commit/8d8e9af40b18e298652004f7f12b1571955e32f8))

## [1.5.0](https://github.com/jacaudi/towonel-operator/compare/v1.4.1...v1.5.0) (2026-06-23)
### Features

* **source:** opt-in cross-namespace auto-routes via towonel.io/auto-routes-namespaces ([#39](https://github.com/jacaudi/towonel-operator/issues/39)) ([#40](https://github.com/jacaudi/towonel-operator/issues/40)) ([f3eb7bd](https://github.com/jacaudi/towonel-operator/commit/f3eb7bd2f089a77036e5bb67a12889729be658ae))

## [1.4.1](https://github.com/jacaudi/towonel-operator/compare/v1.4.0...v1.4.1) (2026-06-22)
### Dependencies

* **deps:** Update GitHub Actions ([70c049a](https://github.com/jacaudi/towonel-operator/commit/70c049a95e309a2436705af68744ab16103844f8))
* **deps:** Update kubernetes-client-libraries ([ef395e0](https://github.com/jacaudi/towonel-operator/commit/ef395e08a7664df6904b97a230f2470b3ca4ee5f))
* **towonel-agent:** Update codeberg.org/towonel/towonel-agent Docker tag to v1 ([4548809](https://github.com/jacaudi/towonel-operator/commit/45488090bd1790ae01ac5d13cd225edf76fac515))

## [1.4.0](https://github.com/jacaudi/towonel-operator/compare/v1.3.0...v1.4.0) (2026-06-22)
### Features

* auto-tunnel HTTPRoutes under a Gateway via towonel.io/auto-routes ([#25](https://github.com/jacaudi/towonel-operator/issues/25)) ([#30](https://github.com/jacaudi/towonel-operator/issues/30)) ([499d1cf](https://github.com/jacaudi/towonel-operator/commit/499d1cf8bfe7f4bd7c033ed1916cb28c2bcd4762))

## [1.3.0](https://github.com/jacaudi/towonel-operator/compare/v1.2.3...v1.3.0) (2026-06-21)
### Features

* default tunnel-ref to the sole TowonelTunnel when omitted ([#29](https://github.com/jacaudi/towonel-operator/issues/29)) ([c65b6d3](https://github.com/jacaudi/towonel-operator/commit/c65b6d3ebf5c1eb16c9f05a57131eff5ffc90e00)), closes [#25](https://github.com/jacaudi/towonel-operator/issues/25) [#24](https://github.com/jacaudi/towonel-operator/issues/24)

## [1.2.3](https://github.com/jacaudi/towonel-operator/compare/v1.2.2...v1.2.3) (2026-06-21)
### Bug Fixes

* reconcile authorized hostnames against hub truth, not status ([#26](https://github.com/jacaudi/towonel-operator/issues/26)) ([#28](https://github.com/jacaudi/towonel-operator/issues/28)) ([5c15d80](https://github.com/jacaudi/towonel-operator/commit/5c15d8023b6b3a4c1e43c5a2cc2a3e4144439703))

## [1.2.2](https://github.com/jacaudi/towonel-operator/compare/v1.2.1...v1.2.2) (2026-06-21)
### Bug Fixes

* re-reconcile sources when a referenced TowonelAgent is created ([#22](https://github.com/jacaudi/towonel-operator/issues/22)) ([#27](https://github.com/jacaudi/towonel-operator/issues/27)) ([6ad6162](https://github.com/jacaudi/towonel-operator/commit/6ad6162643fac5fa335bde106a0156f794dc9ac3))

## [1.2.1](https://github.com/jacaudi/towonel-operator/compare/v1.2.0...v1.2.1) (2026-06-15)
### Bug Fixes

* **controller:** reconcile routing into referenced agents by spec.mode, not the managed-by label ([#18](https://github.com/jacaudi/towonel-operator/issues/18)) ([#21](https://github.com/jacaudi/towonel-operator/issues/21)) ([cd2f2a6](https://github.com/jacaudi/towonel-operator/commit/cd2f2a604a0b5cf8e4bfd475442dce958a382765))

## [1.2.0](https://github.com/jacaudi/towonel-operator/compare/v1.1.0...v1.2.0) (2026-06-15)
### Features

* leader-election RBAC, agent securityContext, API host default, and tunnel reconcile-loop fixes ([#11](https://github.com/jacaudi/towonel-operator/issues/11)-[#14](https://github.com/jacaudi/towonel-operator/issues/14)) ([#19](https://github.com/jacaudi/towonel-operator/issues/19)) ([4a8c1c6](https://github.com/jacaudi/towonel-operator/commit/4a8c1c660689b94ecfa6fec32e9f5fb5b8a33ba7)), closes [#13](https://github.com/jacaudi/towonel-operator/issues/13) [#12](https://github.com/jacaudi/towonel-operator/issues/12) [#12](https://github.com/jacaudi/towonel-operator/issues/12) [#12](https://github.com/jacaudi/towonel-operator/issues/12) [#12](https://github.com/jacaudi/towonel-operator/issues/12)

## [1.1.0](https://github.com/jacaudi/towonel-operator/compare/v1.0.1...v1.1.0) (2026-06-14)
### Features

* HTTPRoute source forwards through parent gateway; edgeTLSMode rename ([#16](https://github.com/jacaudi/towonel-operator/issues/16)) ([5fdce17](https://github.com/jacaudi/towonel-operator/commit/5fdce170df37a465435d5cc7d75a7463c5e41191)), closes [#15](https://github.com/jacaudi/towonel-operator/issues/15)

## [1.0.1](https://github.com/jacaudi/towonel-operator/compare/v1.0.0...v1.0.1) (2026-06-13)
### Bug Fixes

* **controller:** pin default agent image to a tag + Renovate auto-update ([#9](https://github.com/jacaudi/towonel-operator/issues/9)) ([fde816e](https://github.com/jacaudi/towonel-operator/commit/fde816ec03a8ff014da1309b92c7c5ed1c8dfee5))

## 1.0.0 (2026-06-13)
### Features

* **controller:** P3 — TowonelTunnel reconcile (invite/token lifecycle) + envtest ([#3](https://github.com/jacaudi/towonel-operator/issues/3)) ([9a8adf7](https://github.com/jacaudi/towonel-operator/commit/9a8adf7a0e5e0ebd828453a56dd31cb5d0eeacf0))
* **controller:** P4 — TowonelAgent reconcile (workload + routing) + tunnel-side agent aggregation ([#4](https://github.com/jacaudi/towonel-operator/issues/4)) ([e113a12](https://github.com/jacaudi/towonel-operator/commit/e113a12d56df9127c931502846898c462743cc73))
* **controller:** P5 — source/annotation layer (Service/Gateway/HTTPRoute → TowonelAgent) ([#5](https://github.com/jacaudi/towonel-operator/issues/5)) ([3a27157](https://github.com/jacaudi/towonel-operator/commit/3a27157551e43a751e08dda391cb7d4643c1dd32))
* **controller:** P6 — spec.connectivity (iroh direct-path: autodiscover, NodePort, shared node RBAC) ([#6](https://github.com/jacaudi/towonel-operator/issues/6)) ([2c3d08a](https://github.com/jacaudi/towonel-operator/commit/2c3d08a17679ec10dbb2bbacbebbb56a92da6a81)), closes [#1](https://github.com/jacaudi/towonel-operator/issues/1) [#2](https://github.com/jacaudi/towonel-operator/issues/2)
* **operator:** P2 — scaffolding, CRD types, manager, Helm chart, build-only CI ([#2](https://github.com/jacaudi/towonel-operator/issues/2)) ([455643d](https://github.com/jacaudi/towonel-operator/commit/455643d114d28ec74dee4e8869dec6b188ecc30e))
* **towonel:** P1 — Towonel user API client (invites + ports) ([#1](https://github.com/jacaudi/towonel-operator/issues/1)) ([5a341fd](https://github.com/jacaudi/towonel-operator/commit/5a341fd56c0906dd07ef42f6454982c106fc73a2))
