# Changelog

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
