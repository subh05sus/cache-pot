# Security Policy

## Supported versions

Cache-Pot is early-stage software under active development. Security fixes are
applied to the latest release and the `main` branch. Older tagged versions are
not maintained.

| Version | Supported |
|---------|-----------|
| latest (`main`) | Yes |
| older tags | No |

## Reporting a vulnerability

Please do not open a public issue for security problems.

Report vulnerabilities privately using GitHub's
[private vulnerability reporting](https://github.com/subh05sus/cache-pot/security/advisories/new).
If you cannot use that, email the maintainer at the address on the
[GitHub profile](https://github.com/subh05sus).

When reporting, please include:

- A description of the issue and its impact.
- Steps to reproduce, or a proof of concept.
- The Cache-Pot version and your environment (OS, Go version).

## What to expect

- An acknowledgement within a few days.
- An assessment of the report and, if confirmed, a fix on a reasonable timeline.
- Credit in the release notes once a fix ships, unless you prefer to stay anonymous.

## Scope

Cache-Pot has no authentication by default and is intended to run on a trusted
network or `localhost`, like Redis. Exposing it directly to the public internet
without a password (`--auth`) or a firewall is not a supported configuration.
Reports that rely on such a setup may be treated as configuration issues rather
than vulnerabilities.
