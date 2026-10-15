---
title: Keep ecosystem extensions passive and reproduce upstream bugs before reporting
author: Aw338WoWmUI
date: 2026-10-15
touches:
  - ".github/workflows/ci.yml"
  - LICENSE
  - Makefile
  - NOTICE
  - README.md
  - abi.go
  - go.mod
  - go.sum
  - import.go
  - management.go
---

## Decided
Subscription Tools owns only missing management features: native export import, public Copilot normalized quota, confirmed manual Reset Bank and Quota Pacer dry-run application through host field APIs. It registers no scheduler, auth provider, executor or request interceptor. Existing provider plugins and quota collectors stay unchanged.

## Tried and ruled out
Account Config Manager v0.3.1469 cannot preserve our retry settings: isolated tests show it writes Codex request_retry to 6 by default or 0 when attempts is zero, even with no management key. Do not install it only for manual reset. Upstream affinity controls never existed, so their fork-only save integration is not an upstream bug. Only the Codex 168h classification failure was reproduced and submitted upstream.

## Watch out
Public resource routes serve static HTML only; all data and actions require host management authentication. Reset is explicit and never tested with a real credit. Quota Pacer still owns scoring; disable its native auto_apply and other routing-field writers when enabling the bridge. Native-library load plus isolated host tests are required before publication.
