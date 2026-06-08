# jh CLI end-to-end tests

This package contains end-to-end tests for the `jh` CLI. They build the binary
and run its read/GET commands against a **live JuliaHub instance** — by default
the platform nightly instance **`nightly.juliahub.dev`**, which has the General
registry and its packages synced.

The tests are **implemented here** (versioned alongside the CLI) but **executed
by the JuliaHub platform nightly CI**, which deploys the instance and provisions
credentials. See [Running in CI](#running-in-ci).

## Layout

One file per command category, plus shared infrastructure:

| File | Commands |
|------|----------|
| [harness_test.go](harness_test.go) | `TestMain`, binary build, isolated-config auth, `runJH`/`runOK` |
| [helpers_test.go](helpers_test.go) | assertions, output parsers, backend-gap/skip helpers |
| [meta_test.go](meta_test.go) | `--version`, `--help` |
| [auth_test.go](auth_test.go) | `auth status`, `auth env` |
| [user_test.go](user_test.go) | `user info`, `user list`, identity cross-check |
| [group_test.go](group_test.go) | `group list` |
| [project_test.go](project_test.go) | `project list`, `project list --user <name>` |
| [dataset_test.go](dataset_test.go) | `dataset list` → `status`/`download` on the first entry |
| [package_test.go](package_test.go) | `package search`/`info`/`dependency` + not-found |
| [registry_test.go](registry_test.go) | `registry list` → `config`/`registrator`/`permission list` on a real registry |
| [vuln_test.go](vuln_test.go) | `vuln <package>` |
| [scan_test.go](scan_test.go) | `scan <manifest>` (--no-wait) → `scan status`/`results <uuid>` + input validation |
| [job_test.go](job_test.go) | `job list` |
| [admin_test.go](admin_test.go) | `admin user/token/group/credential list`, `landing-page show` |

## How the tests assert (production-ready, not exit-code theatre)

- **Identity** (deterministic on any instance): `auth status` and `user info`
  must report the same server and email the credentials represent, and the two
  commands must agree. Proves auth + a real GraphQL round-trip + correct parsing.
- **Entity chaining**: for commands that need a specific entity, the suite runs
  the `list` command, takes the **first entry**, and drives the detail command
  off it — e.g. `dataset list` → `dataset status`/`download <id>`, and
  `registry list` → `registry config`/`registrator`/`permission list <name>`.
- **Real structure**: listings must match a recognizable shape (`Found N …` /
  `No … found`); detail commands must contain the expected fields (a presigned
  download URL, a registry config `"name"`, a UUID, etc.).
- **Graceful skips on backend gaps, not CLI defects**: when a command is
  unavailable or unauthorized on the instance — missing admin permissions, an
  absent endpoint (404), a disallowed GraphQL query, or a transient timeout —
  the test **skips** via `skipIfUnsupported`. Genuine CLI defects (e.g. a
  response that fails to parse) are deliberately **not** matched by that helper,
  so they still fail.
- **Admin commands** require elevated permissions: under a standard test user
  they skip; run under an admin credential they execute. (See
  [Running in CI](#running-in-ci) about credential selection.)

## Design

- **Build-tag gated.** Every file is tagged `//go:build e2e`, so the suite never
  runs as part of the normal `go test ./...` unit suite. Run it with
  `go test -tags e2e ./e2e/...`.
- **Read-mostly.** The suite exercises read/GET commands, so it is safe against a
  shared instance and needs no cleanup. The one exception is `scan`, which
  submits a manifest scan with `--no-wait` (no long poll, nothing to delete).
- **Adapts to the instance.** Tests assert real data/structure when the relevant
  endpoint is available and **skip** when it is not (absent endpoint, missing
  permissions, disallowed query, timeout). Genuine CLI defects still fail.
- **Skips without credentials.** If no credentials are available, the suite
  skips rather than fails.

## Authentication

The `jh` CLI authenticates **only** from its config file `~/.juliahub` — exactly
as it does after a normal interactive `jh auth login`. The CLI is not modified
for CI; there is no environment-variable auth path.

For non-interactive/CI use, the **test harness** materializes a throwaway
`~/.juliahub` inside an isolated `HOME` from credentials the platform exports,
then runs `jh` against it. This reuses the same credential source the platform
already uses for its other live test suites (e.g. JuliaHub.jl): a headless login
produces an `auth.toml`, and the tokens plus server host are exported as env
vars. The harness translates those into a `~/.juliahub` file; the CLI never reads
the env vars itself.

| Variable                | Read by    | Meaning                                                       |
| ----------------------- | ---------- | ------------------------------------------------------------- |
| `JULIAHUB_SERVER`       | harness    | Target host, e.g. `https://nightly.juliahub.dev` (scheme optional). Written as the `server=` line. |
| `JULIAHUB_ID_TOKEN`     | harness    | OIDC `id_token` from `auth.toml` — written as `id_token=`, used by most `jh` API calls. |
| `JULIAHUB_TOKEN`        | harness    | OAuth `access_token` from `auth.toml` — written as `access_token=` (optional; falls back to the id token). |
| `JULIAHUB_REFRESH_TOKEN`| harness    | Optional `refresh_token`; written as `refresh_token=` if present. |

When these are supplied, the harness writes the isolated config and runs `jh`
with `HOME` pointed at it, so a stale login on the runner cannot interfere and a
developer's real `~/.juliahub` is never touched. When they are absent, the suite
falls back to the real `~/.juliahub` (a developer's existing login).

> **Note:** Most `jh` commands send the **`id_token`** as the bearer token (only
> a few dataset operations use the access token). The platform `auth.toml`
> contains both — make sure the CI step exports `JULIAHUB_ID_TOKEN`, not only
> `JULIAHUB_TOKEN`.

## Running locally

Against your existing `jh auth login` (from the repo root):

```bash
make e2e            # all tests
make e2e-fast       # skips the slow project-list tests (GraphQL can be slow)

# or directly:
go test -tags e2e -v ./e2e/...
go test -tags e2e -v -run TestDataset ./e2e/...     # one category
```

Against a different instance without touching your login — supply explicit
credentials and the harness writes a throwaway isolated config:

```bash
JULIAHUB_SERVER=https://nightly.juliahub.dev \
JULIAHUB_ID_TOKEN="$(jh auth env | sed -n 's/^JULIAHUB_ID_TOKEN=//p')" \
  go test -tags e2e -v ./e2e/...
```

To test a prebuilt binary instead of rebuilding, set `JH_BIN`:

```bash
JH_BIN=./jh go test -tags e2e -v ./e2e/...
```

## Running in CI

These tests run from the **JuliaHub platform** repo's nightly CI, which already
deploys `nightly.juliahub.dev`, provisions test credentials, and has a headless
login (`login-and-get-auth-toml`) that yields an `auth.toml`. The wiring there:

1. A composite action `.github/actions/jh-cli-e2e/` performs the headless login,
   extracts the `id_token` **and** `access_token` from the `auth.toml`, checks
   out this repo, and runs `go test -tags e2e ./e2e/...` with `JULIAHUB_SERVER` /
   `JULIAHUB_ID_TOKEN` / `JULIAHUB_TOKEN` set (the harness turns those into a
   `~/.juliahub`).
2. A `jh-cli-e2e-tests` job in `platform-instance-tests.yml` (gated by a
   `run-jh-cli-tests` input), modelled on the existing `juliahubjl-tests` job.
3. `build-nightly.yml` enables it for the `nightly.juliahub.dev` run.

> **Credential selection.** That job authenticates as the standard **test** user,
> so the `admin *` tests skip there. To exercise them in CI, point the job at the
> **admin** credential (already exposed by `reusable-get-credentials`); admin is a
> superset, so the non-admin tests still pass.
>
> **Export the id token.** Most `jh` commands send the `id_token` as the bearer
> token; only a few dataset operations use the access token. Make sure the CI
> step exports `JULIAHUB_ID_TOKEN`, not only `JULIAHUB_TOKEN`.
