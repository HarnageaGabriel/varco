# ADR 0002: Stable diagnostic codes

## Status

Accepted

## Context

Users and automation need to identify findings without matching prose. Diagnostic documentation must remain synchronized with the implementation.

## Decision

Every diagnostic uses `VAR-<AREA>-NNNN`, where the area is one of `CFG`, `PLAN`, `RUN`, `NET`, `TLS`, `CMP`, or `RPT`. Codes are never reassigned to a different meaning.

The registry in `internal/diag` is the source of truth. `tools/docsgen` generates one page per code plus the diagnostic index, and CI checks for drift.

## Consequences

Adding or changing a diagnostic requires a registry change and regenerated documentation. Consumers may safely key automation and support guidance by code.
