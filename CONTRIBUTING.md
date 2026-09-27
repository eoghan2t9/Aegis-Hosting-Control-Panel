# Contributing to Aegis

Thank you for helping improve Aegis.

## Changes come back to this repository

Aegis is free for noncommercial use under the
[PolyForm Noncommercial License 1.0.0](LICENSE), and commercial use needs a paid
licence (see [`COMMERCIAL.md`](COMMERCIAL.md)).

If you modify Aegis, **we ask that you contribute your changes back** to this
repository as a pull request, so everyone benefits from them. Commercial licences
may require it as a term. Note that the noncommercial licence itself does not
force you to publish private changes, so this is a request for noncommercial
users, not a legal condition.

## Licence of your contribution

By submitting a contribution (a pull request, patch or issue with code), you
confirm that:

1. you wrote it, or have the right to submit it; and
2. it is licensed under the same PolyForm Noncommercial License 1.0.0 as the
   rest of the project; and
3. you grant the project maintainer a perpetual, worldwide, non-exclusive,
   royalty-free right to use, modify and sublicense it — including under
   commercial licences — so the project can keep offering commercial licences.

If you cannot agree to that, please do not submit the contribution.

## Before you open a pull request

```bash
go build ./... && go vet ./... && go test ./...     # must be green
gofmt -l <files you touched>                        # must print nothing
node --check web/js/views/<file>.js                 # syntax check for frontend edits
```

- Keep to the layering: `web/js` → `internal/api` → `internal/svc` → `internal/store`.
  `api` never runs `exec` or writes to `/etc`; `svc` never reads an HTTP request;
  `store` never shells out.
- Every endpoint needs its auth / role / feature guard, an ownership check
  (not-owned returns 404) and an audit entry. Never log or return secrets.
- Validate anything that reaches a shell, a path or SQL. Never do I/O on a
  customer's files as root — use the `Files` methods.
- Schema changes are additive only. Add tests; they must not need root or
  system services.
- Use placeholder names such as `alice`, `bob` and `example.com` in code, tests
  and commit messages — never real account or domain names.

Write commit messages in the imperative, and explain the root cause of a fix,
not just the diff.
