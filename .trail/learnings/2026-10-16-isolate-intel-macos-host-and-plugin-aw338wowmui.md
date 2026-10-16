---
title: Isolate Intel macOS host and plugin Go runtime call threads
author: Aw338WoWmUI
date: 2026-10-16
branch: fix/full-platform-releases
touches:
  - README.md
  - abi.go
  - abi_bridge.c
---

## Decided
For Darwin amd64 only, cross the native ABI on a fresh joined pthread in both directions. Independent host and c-shared Go runtimes otherwise share thread-local Go state and corrupt one another. Other platforms retain direct invocation.

## Tried and ruled out
The full official host and a minimal Go host reproduce bad sweepgen or an invalid callback stack. A C host loading the same minimal library passes. Hiding exported symbols did not fix it. The isolated minimal probe passed 10 times; full imports, duplicate protection, auth boundaries and quota registration passed 10 consecutive official-host starts. Removing only the thread-isolation branch makes the real integration fail again.

## Watch out
Cover init, calls, frees, shutdown and host callbacks. The C invocation is synchronous; never return while the worker holds a pointer to the caller's stack. Do not use runtime/TLS layout hacks or change another plugin's code. Standard JSON import is already a host/Dashboard feature; this plugin's import helper converts native CLI export formats.
