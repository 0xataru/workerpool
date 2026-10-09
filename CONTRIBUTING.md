# Contributing

Thanks for taking the time. A few things worth knowing before you start.

## The scope is small on purpose

The API is two functions and two types, and keeping it that way is a feature.
Before writing code for a new option or entry point, please open an issue to
discuss it. Many requests — retries, rate limiting, fail-fast, progress — are
already covered by wrapping `process`; see the
[Recipes](README.md#recipes) section.

Bug reports, test improvements, documentation fixes and benchmark corrections
are always welcome without asking first.

## Before you open a pull request

```sh
make check   # gofmt, vet, staticcheck, tests with -race — exactly what CI runs
```

For changes to `workerpool.go`, also run:

```sh
make stress  # randomised runs; a failure prints a seed you can replay
make fuzz    # coverage-guided; failures are saved under testdata/fuzz
```

and, if the change could affect speed:

```sh
make bench
```

Pull requests also get an automatic benchmark comparison against `main` in the
job summary. Shared CI runners are noisy, so treat it as a hint and confirm
anything surprising locally with `benchstat` over `-count=10`.

## Tests have to be able to fail

Every guarantee in the README has a test that was checked by deliberately
breaking the implementation. If you add or change a guarantee, do the same:
break the code, watch the test fail, then restore it. A concurrency test that
passes on broken code is worse than no test.

Prefer barriers and channels over `time.Sleep` when testing concurrency — a
test that passes because the machine was fast proves nothing.

## Changelog

Add a line under `[Unreleased]` in [CHANGELOG.md](CHANGELOG.md) for anything a
user would notice. Releases are cut by the maintainer with `make bump` and
`make tag`.
