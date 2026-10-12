# ADR 0004: Endpoint syntax

## Status

Accepted

## Context

The `--old` and `--new` flags need a compact syntax that handles IPv4, IPv6, and explicit ports without ambiguity.

## Decision

Both flags accept an IP address, `IP:port`, or `[IPv6]:port`. An omitted port defaults to 80 for HTTP probes and 443 for HTTPS probes.

Bracketed IPv6 is required when a port is present. Scheme selection belongs to probe configuration rather than being inferred from the port.

## Consequences

Endpoint parsing will reject ambiguous unbracketed IPv6-with-port input and report a configuration diagnostic.
