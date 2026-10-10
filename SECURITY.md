# Security policy

## Reporting a vulnerability

**Please do not open a public issue for security problems.**

Report privately through GitHub: on this repository open **Security → Report a vulnerability**
(private vulnerability reporting). If that is unavailable, contact a maintainer listed on the repository's
front page and ask for a private channel before sharing details.

Please include:

- the RepoMan version (`repoman version`) and how it is deployed,
- what an attacker can do and what they need (anonymous, any account, a specific role),
- steps to reproduce, or a proof of concept.

What to expect:

- an acknowledgement within a few days;
- an assessment and, for confirmed issues, a fix in a new release, with credit to you in the changelog
  unless you prefer otherwise;
- coordinated disclosure: please give us reasonable time to release a fix before publishing details.

## Supported versions

Security fixes are made on the latest release. Upgrading is a matter of replacing one binary
([docs/installation.md](docs/installation.md#upgrading)), so please stay current.

## Scope

In scope: the RepoMan server, its web UI, API, package-protocol endpoints, the container image and the
install scripts in this repository.

Not vulnerabilities by themselves:

- behaviour that requires the **admin** role (administrators are fully trusted, including the ability to make
  the server contact URLs they configure);
- anything that requires read access to the **data directory** or its backups (they hold secrets by design;
  see [docs/security.md](docs/security.md#what-is-stored-and-how));
- anonymous downloads when **anonymous access** is enabled (a documented, switchable default);
- running without TLS against the documentation's advice;
- the content of artifacts stored in or proxied through RepoMan (it is not a scanner).

## More

How RepoMan protects accounts and artifacts, its hardening checklist and its known limits are documented in
[docs/security.md](docs/security.md).
