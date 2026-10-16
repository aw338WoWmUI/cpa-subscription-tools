---
title: Ship and load all store-required native platform libraries
author: Aw338WoWmUI
date: 2026-10-16
branch: fix/full-platform-releases
touches:
  - ".github/workflows/ci.yml"
  - Makefile
  - README.md
  - plugin.go
  - scripts/integration.py
  - scripts/package.py
---

## Decided
The plugin store requires five binary targets: Darwin amd64/arm64, Linux amd64/arm64 and Windows amd64. Build, race-test and load each library in the unmodified host on its matching runner before publishing. Management behavior remains unchanged.

## Tried and ruled out
The v0.1.0 Linux-amd64-only release was rejected in store PR259 despite passing the source and route-boundary audit. Compiling alone is insufficient evidence that a shared library can load on its target platform.

## Watch out
Windows YAML paths require escaped backslashes. Package the library at archive root with LICENSE and NOTICE, merge all five checksums, and gate publication on the whole matrix. Runtime provider plugins and user credentials do not need changes for this packaging update.
