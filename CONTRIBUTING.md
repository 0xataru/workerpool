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

## Workflow

You need Go 1.23 or newer and `make`. Nothing else: staticcheck and benchstat
are fetched on demand by `go run`, and the library has no dependencies.

1. **Fork and clone**, then create a branch:

   ```sh
   git clone https://github.com/<you>/workerpool && cd workerpool
   git switch -c fix/stream-leak
   ```

2. **Make your change**, with a test that fails without it.

3. **Run what CI runs:**

   ```sh
   make fmt     # format the tree
   make check   # gofmt, vet, staticcheck, tests with -race: exactly what CI runs
   ```

4. **For changes to `workerpool.go`**, also run the slower suites:

   ```sh
   make stress  # randomised runs; a failure prints a seed you can replay
   make fuzz    # coverage-guided, 1m per target (FUZZTIME=5m for longer)
   make bench   # if the change could affect speed
   ```

5. **Add a line under `[Unreleased]`** in [CHANGELOG.md](CHANGELOG.md) if users
   would notice the change.

6. **Commit, push and open a pull request** against `main`. Use a short
   [conventional commit](https://www.conventionalcommits.org/) line for the PR
   title, such as `fix: stop Stream leaking a worker on early break`, because
   it becomes the commit message on `main`.

Pull requests also get an automatic benchmark comparison against `main` in the
job summary. Shared CI runners are noisy, so treat it as a hint and confirm
anything surprising locally with `benchstat` over `-count=10`.

### Commands

`make help` lists them all:

| Command | What it does |
| --- | --- |
| `make check` | Everything CI runs: `fmt-check`, `vet`, `lint`, `test` |
| `make test` | The test suite with the race detector |
| `make fmt` | Format the tree with gofmt |
| `make lint` | staticcheck |
| `make stress` | 20 randomised runs with `-race`; failures print a replayable seed |
| `make fuzz` | Fuzz `Map` and `Stream`, `FUZZTIME` each (default 1m) |
| `make bench` | Benchmarks, without `-race` because it distorts timings |
| `make cover` | Coverage of the library package |
| `make build` | Build the package and the examples |
| `make clean` | Remove build and coverage artefacts |
| `make bump VERSION=X.Y.Z` | Maintainer only: prepare a release |
| `make tag` | Maintainer only: tag the release |

## How pull requests are merged

`main` is protected, and the rules are enforced by GitHub rather than by
convention:

- **Every change goes through a pull request.** Nobody pushes to `main`
  directly, including the maintainer.
- **The maintainer must approve it.** [`CODEOWNERS`](.github/CODEOWNERS) makes
  @0xataru the required reviewer for every file, and other approvals do not
  count toward merging.
- **Pushing after approval resets it.** A new commit dismisses the approval, so
  what gets merged is exactly what was reviewed.
- **CI must pass:** tests on Go 1.23 and stable, tests with `GOMAXPROCS` 1, 2 and
  4, and staticcheck. The benchmark comparison is informational and never
  blocks a merge.
- **The branch must be up to date with `main`.** If `main` moved on, rebase or
  use the *Update branch* button.
- **Every review thread must be resolved** before merging.
- **History is linear.** Pull requests are squash- or rebase-merged, so keep the
  title a short [conventional commit](https://www.conventionalcommits.org/)
  line, such as `fix: stop Stream leaking a worker on early break`. It becomes
  the commit message on `main`.
- **Release tags are immutable.** Once a `vX.Y.Z` tag is pushed it cannot be
  moved or deleted, because the Go module proxy has already published it.

## Tests have to be able to fail

Every guarantee in the README has a test that was checked by deliberately
breaking the implementation. If you add or change a guarantee, do the same:
break the code, watch the test fail, then restore it. A concurrency test that
passes on broken code is worse than no test.

Prefer barriers and channels over `time.Sleep` when testing concurrency — a
test that passes because the machine was fast proves nothing.

## Releasing (maintainer)

`main` is protected, so a release goes through a pull request like any other
change. The tag is created afterwards, on the merged commit.

1. **Prepare the release on a branch.** `[Unreleased]` in the changelog must
   have entries, or `make bump` refuses:

   ```sh
   git switch main && git pull
   git switch -c release/v0.1.0
   make bump VERSION=0.1.0   # updates VERSION, CHANGELOG.md and the README badge
   git commit -am "chore: release v0.1.0"
   git push -u origin release/v0.1.0
   ```

2. **Open a pull request, wait for CI, and squash-merge it.**

3. **Tag the merged commit and push the tag:**

   ```sh
   git switch main && git pull
   make tag                  # runs make check, then tags HEAD as v0.1.0
   git push origin v0.1.0
   ```

4. **Publish the GitHub release** with the changelog section as notes:

   ```sh
   gh release create v0.1.0 --title v0.1.0 --notes "..."
   ```

Pushing the tag is the real release: once `v0.1.0` is on GitHub, the Go module
proxy can serve it, and the tag can never be moved or deleted. Run `make tag`
only on the exact commit you want to publish.
