# ADR 0003: Probe safety defaults

## Status

Accepted

## Context

Probes send traffic to user-selected endpoints. Defaults must minimize unintended state changes, traffic amplification, and misleading TLS results.

## Decision

- Only `GET`, `HEAD`, and `OPTIONS` are safe by default. Any other method requires explicit opt-in for that case and run.
- Redirects are never followed. A redirect response is compared as received.
- TLS certificate verification is disabled only for probes so certificate details can be captured. Certificate trust is not reported as pass or fail.
- Rate limiting is always enabled, though its configured rate may vary.
- Requests are sent only to endpoints named by the user.

## Consequences

The probe layer must enforce these rules centrally. Changes to request sending or TLS behavior require adversarial review under `CONVENTIONS.md`.
