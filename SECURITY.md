# Security policy

## Supported versions

Only the latest release receives fixes. Before 1.0.0 there are no backports:
upgrade to the newest `v0.x` to get a fix.

## Reporting a vulnerability

Please **do not open a public issue.** Report it privately through
[GitHub's vulnerability reporting](https://github.com/0xataru/workerpool/security/advisories/new)
instead.

Include what you can of:

- the version or commit you tested
- a minimal program that reproduces the problem
- what an attacker gains — for a library like this, usually a deadlock, a
  goroutine leak or unbounded memory growth driven by untrusted input

You should get a first response within a week. Once a fix is released, the
advisory is published with credit to you, unless you prefer otherwise.
