# Wiring the jh CLI e2e tests into JuliaHub platform nightly CI

The test suite lives in this repo ([`../`](../)), but it is meant to run as part
of the **JuliaHub platform** nightly CI, which deploys a fresh instance and
provisions credentials. This directory contains the platform-side glue to add to
the `JuliaHubOpenAPI`/`JuliaHub` repo via a separate PR.

## What the platform CI already provides

- A nightly deploy to `nightly.juliahub.dev`
  (`.github/workflows/build-nightly.yml`, cron `30 1 * * 1-5`).
- A reusable test workflow `.github/workflows/platform-instance-tests.yml` that
  already runs Playwright UI/API tests and the **JuliaHub.jl** live client tests.
- Credential provisioning: `.github/workflows/reusable-get-credentials.yml`
  (GPG-encrypted shared password + per-host test emails).
- A headless login that produces an `auth.toml`:
  `.github/actions/login-and-get-auth-toml`.

The jh CLI uses the **same** credential mechanism as the JuliaHub.jl job, so
hooking in is mostly copy-and-adapt.

## Steps

1. **Add the composite action.** Copy [`jh-cli-e2e/`](./jh-cli-e2e) into
   `JuliaHub/.github/actions/jh-cli-e2e/`. It performs the headless login,
   extracts the `id_token` and `access_token` from the resulting `auth.toml`,
   checks out this repo (`JuliaComputing/jh`), and runs
   `go test -tags e2e ./e2e/...` with the env contract the CLI expects.

2. **Add a job.** Add the job in [`example-job.yml`](./example-job.yml) to
   `platform-instance-tests.yml`, alongside `juliahubjl-tests`. It decrypts the
   shared test password and calls the composite action.

3. **(Optional) Add a workflow input** `run-jh-cli-tests` so the job can be
   toggled per-trigger, defined the same way as the existing
   `run-juliahubjl-tests` input, and gate the job with
   `if: ${{ inputs.run-jh-cli-tests }}`.

4. **(Optional) Report to Slack.** If you want the result in the nightly Slack
   summary, add the job's `outcome` output to the existing report step the same
   way `juliahubjl-tests.outputs.outcome` is consumed.

## Notes

- **File-based auth only.** The `jh` CLI authenticates exclusively from
  `~/.juliahub`. The test harness writes a throwaway `~/.juliahub` into an
  isolated `HOME` from the env vars the composite action exports
  (`JULIAHUB_SERVER`, `JULIAHUB_ID_TOKEN`, `JULIAHUB_TOKEN`) and runs `jh`
  against it — the CLI itself never reads those env vars.
- **Which token?** Most `jh` commands authenticate with the **`id_token`**; only
  a few dataset operations use the `access_token`. The composite action exports
  both (`JULIAHUB_ID_TOKEN`, `JULIAHUB_TOKEN`) — make sure not to drop the id
  token.
- **Read-only.** The suite is read-only and creates nothing, so it is safe to run
  against the shared nightly instance and needs no cleanup job.
- **Pinning.** `jh-ref` defaults to `main`. Pin it to a released tag once the CLI
  cuts releases, the same way the JuliaHub.jl job pins `juliahub-client-ref`.
- **Self-test.** The jh repo's own CI compile-checks the suite under the `e2e`
  build tag (`go test -tags e2e -run '^$'`), so it cannot bitrot between nightly
  runs.
