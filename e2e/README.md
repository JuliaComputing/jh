# jh CLI end-to-end tests

This package contains end-to-end tests for the `jh` CLI. They build the binary
and run its read/GET commands against a **live JuliaHub instance** — by default
the platform nightly instance **`nightly.juliahub.dev`**, which has the General
registry and its packages synced.

The tests are **implemented here** (versioned alongside the CLI) but **executed
by the JuliaHub platform nightly CI**, which deploys the instance and provisions
credentials. See [`ci/`](./ci).

## Layout

One file per command category, plus the shared harness:

| File | Commands |
|------|----------|
| [harness_test.go](harness_test.go) | build/run helpers, isolated-config auth, output parsers, skip helpers |
| [meta_test.go](meta_test.go) | `--version`, `--help` |
| [auth_test.go](auth_test.go) | `auth status`, `auth env` |
| [user_test.go](user_test.go) | `user info`, `user list`, identity cross-check |
| [group_test.go](group_test.go) | `group list` |
| [project_test.go](project_test.go) | `project list`, `project list --user <name>` |
| [dataset_test.go](dataset_test.go) | `dataset list` → `status`/`download` on the first entry |
| [package_test.go](package_test.go) | `package search`/`info`/`dependency` + not-found |
| [registry_test.go](registry_test.go) | `registry list` → `config`/`registrator`/`permission list` on a real registry |
| [vuln_test.go](vuln_test.go) | `vuln <package>` |
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
  they skip; run under an admin credential they execute. (See the note in
  `ci/README.md` about credential selection.)

## Design

- **Build-tag gated.** Every file is tagged `//go:build e2e`, so the suite never
  runs as part of the normal `go test ./...` unit suite. Run it with
  `go test -tags e2e ./e2e/...`.
- **GET / read-only.** The suite exercises only non-mutating commands, so it is
  safe against a shared instance and needs no cleanup. (The CLI has no
  `dataset delete`, so creating data would otherwise leave orphans.)
- **Skips without credentials.** If no credentials are available, the suite
  skips rather than fails.

> **Note on instance data.** The catalog tests assume the General registry and
> common packages are synced — true for `nightly.juliahub.dev`. Pointed at a bare
> instance (one with no registries synced), those tests will fail by design; the
> identity and error-path groups still pass anywhere.

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

Against your existing login:

```bash
go test -tags e2e -v ./e2e/...
```

With explicit credentials (matches the CI contract):

```bash
JULIAHUB_SERVER=https://nightly.juliahub.dev \
JULIAHUB_ID_TOKEN=<id_token> \
JULIAHUB_TOKEN=<access_token> \
  go test -tags e2e -v ./e2e/...
```

To test a prebuilt binary instead of building from source, set `JH_BIN`:

```bash
JH_BIN=./jh go test -tags e2e -v ./e2e/...
```

## Running from the JuliaHub platform nightly CI

The platform CI already has everything needed:

1. A deployed nightly instance (`nightly.juliahub.dev`).
2. Provisioned test credentials (`.github/workflows/reusable-get-credentials.yml`).
3. A headless login that yields an `auth.toml`
   (`.github/actions/login-and-get-auth-toml`).

To run this suite, add a job that checks out this repo, logs in to get an
`auth.toml`, extracts the `id_token` (and `access_token`), and runs
`go test -tags e2e`. A ready-to-use composite action and an example job are in
[`ci/`](./ci):

- [`ci/jh-cli-e2e/action.yml`](./ci/jh-cli-e2e/action.yml) — composite action to
  copy into `JuliaHub/.github/actions/jh-cli-e2e/`.
- [`ci/example-job.yml`](./ci/example-job.yml) — example job to add to
  `platform-instance-tests.yml`, modeled on the existing `juliahubjl-tests` job.

See [`ci/README.md`](./ci/README.md) for step-by-step integration instructions.
