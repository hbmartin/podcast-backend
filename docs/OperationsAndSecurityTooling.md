# Operations and security tooling

## Purpose

This workstream hardens the container health probe, removes a committed debug
binary, makes generated output reproducible, and turns recurring review findings
into CI policy.

## TLS health probe

When `TLS_CERT_FILE` and `TLS_CERT_KEY_FILE` enable HTTPS, the local health probe
now trusts the configured certificate instead of disabling certificate
verification.

The probe:

- loads certificates from `TLS_CERT_FILE` as its trust roots;
- parses the first certificate as the leaf;
- selects the first DNS SAN, or first IP SAN when no DNS SAN exists, as the
  verification server name;
- requires at least TLS 1.2;
- rejects HTTP redirects; and
- retains a five-second request timeout.

The certificate must contain a DNS or IP Subject Alternative Name. A Common
Name without a SAN is insufficient.

### Health-probe user impact

There is no application API change. A misconfigured TLS certificate can now
make the container unhealthy instead of being silently accepted, which prevents
traffic from reaching an instance whose local TLS identity is invalid.

### Health-probe operator impact

Validate the certificate file, chain, SANs, and key pairing before rollout. The
probe reports configuration failures on stderr and exits nonzero. Redirecting
`/health` is no longer compatible with the container probe.

## Tracked binary policy

The 42 MB root-level ELF file `main` was removed from the repository tip and
`/main` is ignored for local builds. Its historical Git objects remain; history
rewriting was intentionally not performed.

`scripts/check-no-tracked-binaries.sh` examines every Git-tracked regular file
and fails when it finds:

- a file larger than 10 MiB;
- an ELF executable;
- a Mach-O executable; or
- a PE32 executable.

This is a repository policy, not just a check for one filename. A legitimate
large artifact must be stored outside Git or accompanied by an explicit policy
change.

## CI security gates

The container workflow now runs:

1. the tracked-binary check;
2. `go vet ./...`;
3. Semgrep 1.151.0 with the repository policy;
4. gosec 2.28.0 scoped to G402 TLS verification; and
5. govulncheck 1.6.0 for reachable Go vulnerabilities.

The eight Semgrep rules guard against:

- disabled TLS verification;
- default HTTP clients at network boundaries;
- fail-open block checks;
- byte-unsafe truncation;
- queue or network work inside transactions;
- unbounded request-loop goroutines;
- moderation of a different quote than the stored value; and
- raw internal error text in HTTP responses.

Run all security checks locally with:

```sh
make security
scripts/check-no-tracked-binaries.sh
```

Semgrep must be installed separately for local use; CI pins it with pip.

## Dependency and code-generation reproducibility

- `protoc-gen-go` is pinned to 1.36.11.
- `sqlc` is pinned to 1.31.1.
- gosec and govulncheck are invoked at pinned versions from the make targets.
- `golang.org/x/text` was upgraded to 0.39.0 after a reachable vulnerability
  was identified during verification.

Regenerate derived files only through:

```sh
make proto
make sqlc
```

Review generated diffs together with their source `.proto` or `.sql` change.

## User impact

Most changes are preventive and have no wire-level effect. Users benefit from
fewer insecure deployments, dependency vulnerabilities, and regressions of the
specific authorization, network, and truncation defects fixed in this work.

## Operator impact

- CI takes longer and requires Python/pip for the pinned Semgrep install.
- New findings are blocking because Semgrep runs with `--error`.
- Govulncheck distinguishes reachable vulnerabilities from advisories in modules
  that the program does not call; evaluate both, but block on reachable paths.
- The binary policy may catch build products accidentally added under names
  other than `main`.
- Health-check failures after deployment should be investigated as certificate
  trust/SAN problems, not bypassed with insecure TLS settings.

## Rollback considerations

Removing CI gates or restoring insecure health probing does not require a schema
change, but it removes controls designed to prevent recurrence. If a rule is too
broad, narrow it with a documented, line-specific suppression rather than
disabling the entire policy.
