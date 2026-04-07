# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is a Go-based CLI tool for interacting with JuliaHub, a platform for Julia computing. The CLI provides commands for authentication, dataset management, registry management, package management, project management, user information, token management, registry credential management, landing page management, Git integration, and Julia integration.

## Architecture

The application follows a command-line interface pattern using the Cobra library with a modular file structure:

- **main.go**: Core CLI structure with command definitions and configuration management
- **auth.go**: OAuth2 device flow authentication with JWT token handling
- **datasets.go**: Dataset operations (list, download, upload, status) with REST API integration
- **registries.go**: Registry operations (list, config, add, update, registrator) with REST API integration
- **packages.go**: Package operations (search, dependency) with REST API primary path (`/packages/info`), GraphQL fallback, and documentation API (`/docs/{registry}/{package}/stable/pkg.json`)
- **projects.go**: Project management using GraphQL API with user filtering
- **user.go**: User information retrieval using GraphQL API and REST API for listing users
- **tokens.go**: Token management operations (list) with REST API integration
- **credentials.go**: Registry credential management (list, add, update, delete) with REST API integration
- **landing.go**: Landing page management (show, update, remove) with REST API integration
- **git.go**: Git integration (clone, push, fetch, pull) with JuliaHub authentication
- **julia.go**: Julia installation and management
- **run.go**: Julia execution with JuliaHub configuration
- **Configuration**: Uses `~/.juliahub` file for server and token storage

### Key Components

1. **Authentication System** (`auth.go`):
   - Implements OAuth2 device flow for JuliaHub authentication
   - JWT token parsing and validation with automatic refresh
   - Supports multiple server environments (juliahub.com, custom servers)
   - Stores tokens securely in `~/.juliahub` with 0600 permissions

2. **API Integration**:
   - **REST API**: Used for dataset operations (`/api/v1/datasets`, `/datasets/{uuid}/url/{version}`), registry operations (`/api/v1/registry/registries/descriptions`, `/api/v1/registry/config/registry/{name}`, `/api/v1/registry/config/registrator/{name}`), package search/info primary path (`/packages/info`), token management (`/app/token/activelist`), user management (`/app/config/features/manage`), admin group management (`/app/config/groups`), registry credential management (old API tried first, new API fallback), and landing page management (`/app/homepage` GET, `/app/config/homepage` POST/DELETE)
   - **GraphQL API**: Used for projects, user info, user list (`public_users`), group list, and package search/info fallback (`/v1/graphql`)
   - **Headers**: All GraphQL requests require `Authorization: Bearer <id_token>`, `X-Hasura-Role: jhuser`, and `X-Juliahub-Ensure-JS: true`
   - **Authentication**: Uses ID tokens (`token.IDToken`) for API calls

3. **Command Structure**:
   - `jh auth`: Authentication commands (login, refresh, status, env)
   - `jh dataset`: Dataset operations (list, download, upload, status)
   - `jh registry`: Registry operations (list, config — all via REST API)
   - `jh registry config`: Show registry JSON config by name; subcommands add/update accept JSON via stdin or `--file`
   - `jh registry permission`: Registry permission management (list, set, remove)
   - `jh registry registrator`: Show registrator config by name; subcommand update accepts JSON via stdin or `--file`
   - `jh package`: Package search and dependency (REST primary via `/packages/info`, GraphQL fallback; dependency data from `/docs/{registry}/{package}/stable/pkg.json`)
   - `jh project`: Project management (list with GraphQL, supports user filtering)
   - `jh user`: User information (info, list via GraphQL `public_users`)
   - `jh group`: Group information (list via GraphQL)
   - `jh admin`: Administrative commands (user management, token management, group management, credential management, landing page)
   - `jh admin user`: User management (list all users with REST API, supports verbose mode)
   - `jh admin token`: Token management (list all tokens with REST API, supports verbose mode)
   - `jh admin group`: Group management (list all groups via REST API)
   - `jh admin credential`: Registry credential management (list, add, update, delete via REST API)
   - `jh admin credential list`: List all registry credentials (tokens, SSH keys, GitHub Apps); supports verbose mode
   - `jh admin credential add`: Add a credential — subcommands: `token`, `ssh`, `github-app`; accepts JSON argument or stdin
   - `jh admin credential update`: Update a credential — subcommands: `token`, `ssh`, `github-app`; accepts JSON argument or stdin
   - `jh admin credential delete`: Delete a credential — subcommands: `token`, `ssh`, `github-app`; takes positional identifier
   - `jh admin landing-page`: Landing page management (show/update/remove custom markdown landing page with REST API)
   - `jh clone`: Git clone with JuliaHub authentication and project name resolution
   - `jh push/fetch/pull`: Git operations with JuliaHub authentication
   - `jh git-credential`: Git credential helper for seamless authentication
   - `jh julia`: Julia installation management
   - `jh run`: Julia execution with JuliaHub configuration
   - `jh run setup`: Setup Julia credentials without starting Julia

4. **Data Models**:
   - UUID strings for most entity IDs (projects, datasets, resources)
   - Integer IDs for user-related entities
   - Custom JSON unmarshaling for flexible date parsing (`CustomTime`)
   - GraphQL request/response structures with proper operation names

## Development Commands

### Build and Run
```bash
go build -o jh
./jh --help
```

### Run directly
```bash
go run . --help
```

### Code quality checks (always run before commits)
```bash
go fmt ./...
go vet ./...
go build
```

### Test authentication flow
```bash
go run . auth login -s juliahub.com
```

### Test dataset operations
```bash
go run . dataset list
go run . dataset download <dataset-name>
go run . dataset upload --new ./file.tar.gz
```

### Test package operations
```bash
go run . package search dataframes
go run . package search --verbose plots
go run . package search --limit 20 ml
go run . package search --registries General optimization
go run . package info DataFrames
go run . package info Plots --registries General

# Get package dependencies
go run . package dependency DataFrames
go run . package dependency DataFrames --indirect
go run . package dependency CSV --registry General
```

### Test registry operations
```bash
go run . registry list
go run . registry list --verbose
go run . registry config JuliaSimRegistry
go run . registry config JuliaSimRegistry -s nightly.juliahub.dev

# Add a registry (JSON via stdin or --file)
echo '{
  "name": "MyRegistry",
  "license_detect": true,
  "artifact": {"download": true},
  "docs": {"download": true, "docgen_check_installable": false, "html_size_threshold_bytes": null},
  "metadata": {"download": true},
  "pkg": {"download": true, "static_analysis_runs": []},
  "enabled": true, "display_apps": true, "owner": "", "sync_schedule": null,
  "download_providers": [{
    "type": "cacheserver", "host": "https://pkg.juliahub.com",
    "credential_key": "JC Auth Token",
    "server_type": "", "github_credential_type": "", "api_host": "", "url": "", "user_name": ""
  }]
}' | go run . registry config add
go run . registry config add --file registry.json

# Update an existing registry (same JSON schema, same flags)
go run . registry config update --file registry.json

# Show registrator config for a registry
go run . registry registrator MyRegistry

# Update registrator config (JSON via stdin or --file)
echo '{
  "enabled": true,
  "email": "pkg@example.com",
  "authorization": true,
  "ssl_verify": true,
  "registry_fork_url": null,
  "registry_deps": ["General"]
}' | go run . registry registrator update MyRegistry
go run . registry registrator update MyRegistry --file registrator.json

# Get, edit, push back
go run . registry registrator MyRegistry > registrator.json
go run . registry registrator update MyRegistry --file registrator.json
```

### Test project and user operations
```bash
go run . project list
go run . project list --user
go run . project list --user john
go run . user info
go run . user list
go run . group list
go run . admin user list
go run . admin user list --verbose
go run . admin group list
```

### Test token operations
```bash
go run . admin token list
go run . admin token list --verbose
TZ=America/New_York go run . admin token list --verbose  # With specific timezone
```

### Test credential operations
```bash
go run . admin credential list
go run . admin credential list --verbose

# Add credentials (JSON as argument or piped via stdin)
go run . admin credential add token '{"name":"MyToken","url":"https://github.com","value":"ghp_xxxx"}'
go run . admin credential add ssh '{"host_key":"github.com ssh-ed25519 AAAA...","private_key_file":"/home/user/.ssh/id_ed25519"}'
go run . admin credential add github-app '{"app_id":"12345","url":"https://github.com/my-org","private_key_file":"app.pem"}'

# Update credentials (partial update: only supply fields to change)
go run . admin credential update token '{"name":"MyToken","url":"https://github.com/new-org"}'
go run . admin credential update ssh '{"index":1,"private_key_file":"/home/user/.ssh/new_key"}'
go run . admin credential update github-app '{"app_id":"12345","private_key_file":"new_app.pem"}'

# Delete credentials
go run . admin credential delete token MyToken
go run . admin credential delete ssh 1
go run . admin credential delete github-app 12345
```

### Test landing page operations
```bash
go run . admin landing-page show
go run . admin landing-page update '# Welcome to JuliaHub'
go run . admin landing-page update --file landing.md
cat landing.md | go run . admin landing-page update
go run . admin landing-page remove
```

### Test Git operations
```bash
go run . clone john/my-project  # Clone from another user
go run . clone my-project       # Clone from logged-in user
go run . push
go run . fetch
go run . pull
```

### Test Git credential helper
```bash
# Setup credential helper (one-time setup)
go run . git-credential setup

# Test credential helper manually
echo -e "protocol=https\nhost=juliahub.com\npath=git/projects/test/test\n" | go run . git-credential get

# After setup, standard Git commands work seamlessly
git clone https://juliahub.com/git/projects/username/project.git
```

### Test Julia integration
```bash
# Install Julia (if not already installed)
go run . julia install

# Setup Julia credentials only
go run . run setup

# Run Julia REPL with credentials setup
go run . run

# Run Julia with credentials setup
go run . run -- -e "println(\"Hello from JuliaHub!\")"

# Run Julia script with project
go run . run -- --project=. script.jl

# Run Julia with multiple flags
go run . run -- --project=. --threads=4 -e "println(Threads.nthreads())"
```

## Dependencies

- `github.com/spf13/cobra`: CLI framework
- Standard library packages for HTTP, JSON, file I/O, multipart uploads

## Server Configuration

The CLI supports multiple JuliaHub environments:
- Default: `juliahub.com` (uses `auth.juliahub.com` for auth)
- Custom servers: Direct server specification
- Server normalization: Automatically appends `.juliahub.com` to short names

## Authentication Flow

The application uses OAuth2 device flow:
1. Request device code from `/dex/device/code`
2. Present verification URL to user
3. Poll `/dex/token` endpoint until authorization complete
4. Store tokens in configuration file with JWT claims extraction

## API Patterns

### GraphQL Integration
- **Endpoint**: `https://server/v1/graphql`
- **Required headers**: `Authorization: Bearer <id_token>`, `X-Hasura-Role: jhuser`
- **Request structure**: `{operationName: "...", query: "...", variables: {...}}`
- **User ID retrieval**: Projects use actual user ID from `getUserInfo()` call

### REST API Integration
- **Dataset operations**: Use presigned URLs for upload/download
- **Registry operations**: `/api/v1/registry/registries/descriptions` for listing registries
- **User management**: `/app/config/features/manage` endpoint for listing all users
- **Token management**: `/app/token/activelist` endpoint for listing all API tokens
- **Registry credentials**: `GET /api/v1/sysconfig/credentials` to fetch (returns `CredentialsInfo` object directly); `POST` to add tokens/apps; `PUT` to update tokens/apps or replace all SSH credentials; `DELETE` with `{tokens:[...], githubApps:[...]}` to delete
- **Package search/info primary**: `/packages/info` endpoint with `name`, `registries`, `tags`, `licenses`, `limit`, `offset` query params; returns `{packages: [...], meta: {total: N}}`
- **Package dependencies**: `/docs/{registry}/{package}/stable/pkg.json` for dependency information
- **Authentication**: Bearer token with ID token
- **Upload workflow**: 3-step process (request presigned URL, upload to URL, close upload)
- **Credential write pattern**: Targeted mutations via `POST`/`PUT`/`DELETE`; SSH operations require read-modify-write (fetch + full-replacement `PUT`) since `sshcreds` in PUT is a full replacement

### Data Type Handling
- Project/dataset IDs are UUID strings, not integers
- User IDs are integers
- Custom time parsing handles multiple date formats
- Flexible dataset identifier resolution (UUID, name, user/name)

## Git Integration

The CLI provides seamless Git integration with JuliaHub authentication through two approaches:

### Method 1: JuliaHub CLI Wrapper Commands
- **Clone**: `jh clone [username/]project` - resolves project names to UUIDs and clones with authentication
  - Format: `jh clone username/project` or `jh clone project` (defaults to logged-in user)
- **Push/Fetch/Pull**: `jh push/fetch/pull [args...]` - wraps Git commands with authentication headers
- **Authentication**: Uses `http.extraHeader="Authorization: Bearer <id_token>"` for Git operations
- **Argument passthrough**: All Git arguments are passed through to underlying commands
- **Folder naming**: Clone automatically renames UUID folders to project names
- **Conflict resolution**: Handles folder naming conflicts with automatic numbering

### Method 2: Git Credential Helper (Recommended)
- **Setup**: `jh git-credential setup` - configures Git to use JuliaHub CLI as credential helper
- **Multi-server support**: Automatically handles different JuliaHub instances
- **Automatic authentication**: Prompts for authentication when server doesn't match stored config
- **Standard Git commands**: Use `git clone`, `git push`, `git pull`, etc. directly without `jh` wrapper
- **Non-intrusive**: Only handles JuliaHub URLs, other URLs passed to other credential helpers
- **Protocol compliance**: Follows Git credential helper protocol with `get`, `store`, `erase` actions

#### Git Credential Helper Usage:
```bash
# One-time setup
jh git-credential setup

# Then use standard Git commands
git clone https://juliahub.com/git/projects/username/project.git
git push origin main
git pull origin main

# Works with multiple JuliaHub servers automatically
git clone https://internal.juliahub.com/git/projects/user/repo.git  # Auto-prompts for auth
git clone https://custom.juliahub.com/git/projects/user/repo.git    # Auto-prompts for auth
git clone https://github.com/user/repo.git                          # Ignored by helper
```

#### Git Credential Helper Implementation:
- **Domain detection**: Recognizes `*.juliahub.com` and configured custom servers
- **Server matching**: Compares requested host against `~/.juliahub` server field
- **Automatic login**: Runs OAuth2 device flow when server mismatch detected
- **Token management**: Stores and refreshes tokens per server automatically
- **Error handling**: Graceful fallback to other credential helpers for non-JuliaHub URLs

## Package Management

The CLI provides comprehensive package discovery and dependency analysis:

### Package Search and Info
- **Search**: `jh package search` uses GraphQL API to search packages across registries
- **Info**: `jh package info` retrieves detailed package metadata
- **Filtering**: Supports filtering by registry, installation status, and failures

### Package Dependency (`jh package dependency`)
- **Endpoint**: Uses package documentation API at `/docs/{registry}/{package}/stable/pkg.json`
- **Registry resolution**: Automatically uses first registry package belongs to, or specific registry via `--registry` flag
- **Dependency types**: Distinguishes between direct and indirect dependencies via `direct` field in API response
- **Display limits**:
  - Default: Shows up to 10 direct dependencies
  - With `--indirect`: Shows up to 10 direct and 50 indirect dependencies
- **Output format**:
  - Direct-only mode: Single table with columns: NAME, REGISTRY, UUID, VERSIONS
  - Indirect mode: Separate sections for direct and indirect dependencies with columns: NAME, REGISTRY, UUID, VERSIONS
  - Registry column shows which registry each dependency belongs to (empty for stdlib packages)

#### Implementation Details (`packages.go`)
- `getPackageDependencies()`: Main function for dependency retrieval
  1. Fetches all registries to get registry IDs for GraphQL query
  2. Searches for package using GraphQL to get registry information
  3. Determines target registry (first registry or user-specified)
  4. Fetches package documentation JSON from docs endpoint
  5. Filters and limits dependencies based on flags
  6. Displays results in formatted tables with separate sections

#### Data Structures
- `PackageDependency`: Represents a single dependency with fields for direct/indirect status, name, UUID, versions, registry, and slug
- `PackageDocsResponse`: Response from documentation API containing package metadata and dependencies array

## Julia Integration

The CLI provides Julia installation and execution with JuliaHub configuration:

### Julia Installation (`jh julia install`)
- Cross-platform installation (Windows via winget, Unix via official installer)
- Installs latest stable Julia version

### Julia Credentials
- **Authentication file**: Automatically creates `$JULIA_DEPOT_PATH/servers/<server>/auth.toml` (or `~/.julia/servers/<server>/auth.toml` if `JULIA_DEPOT_PATH` is not set)
- **Depot path detection**: Respects `JULIA_DEPOT_PATH` environment variable, uses first path if multiple are specified
- **Atomic writes**: Uses temporary file + rename for safe credential updates
- **Automatic updates**: Credentials are automatically refreshed when:
  - User runs `jh auth login`
  - User runs `jh auth refresh`
  - Token is refreshed via `ensureValidToken()`
  - User runs `jh run` or `jh run setup`

### Julia Commands

#### `jh run [-- julia-args...]` - Run Julia with JuliaHub configuration
```bash
jh run                                    # Start Julia REPL
jh run -- script.jl                       # Run a script
jh run -- -e "println(\"Hello\")"         # Execute code
jh run -- --project=. --threads=4 script.jl # Run with flags
```
- Sets up credentials, then starts Julia
- Arguments after `--` are passed directly to Julia without modification
- User controls all Julia flags (including `--project`, `--threads`, etc.)
- Environment variables set:
  - `JULIA_PKG_SERVER`: Points to your JuliaHub server
  - `JULIA_PKG_USE_CLI_GIT`: Set to `true` for Git integration

#### `jh run setup` - Setup credentials only (no Julia execution)
```bash
jh run setup
```
- Creates/updates `$JULIA_DEPOT_PATH/servers/<server>/auth.toml` with current credentials (or `~/.julia/servers/<server>/auth.toml` if not set)
- Does not start Julia
- Useful for explicitly updating credentials

## Development Notes

- All ID fields in GraphQL responses should be typed correctly (string for UUIDs, int64 for user IDs)
- GraphQL queries are embedded at compile time using `go:embed` from `.gql` files (`userinfo.gql`, `users.gql`, `groups.gql`, `projects.gql`)
- Error handling includes both HTTP and GraphQL error responses
- Token refresh is automatic via `ensureValidToken()`
- File uploads use multipart form data with proper content types
- Julia auth files use TOML format with `preferred_username` from JWT claims
- Julia auth files use atomic writes (temp file + rename) to prevent corruption
- Julia credentials respect `JULIA_DEPOT_PATH` environment variable (uses first path if multiple are specified)
- Julia credentials are automatically updated after login and token refresh
- Git commands use `http.extraHeader` for authentication and pass through all arguments
- Git credential helper provides seamless authentication for standard Git commands
- Multi-server authentication handled automatically via credential helper
- Project filtering supports `--user` parameter for showing specific user's projects or own projects
- Clone command automatically resolves `username/project` format to project UUIDs
- Clone command supports `project` (without username) and defaults to the logged-in user's username
- Folder naming conflicts are resolved with automatic numbering (project-1, project-2, etc.)
- Credential helper follows Git protocol: responds only to JuliaHub URLs, ignores others
- `jh user list` uses GraphQL `public_users` query (via `users.gql`) and displays `<name> (<username>)` per line
- `jh group list` uses GraphQL groups query (via `groups.gql`) and displays one group name per line
- Admin user list command (`jh admin user list`) uses REST API endpoint `/app/config/features/manage` which requires appropriate permissions
- Admin group list command (`jh admin group list`) uses REST API endpoint `/app/config/groups` which requires appropriate permissions
- Admin user list output is compact by default (`<name> (<email>)`); use `--verbose` flag for detailed information (UUID, groups, features)
- Registry list output is concise by default (UUID and Name only); use `--verbose` flag for detailed information (owner, creation date, package count, description)
- Registry config command (`jh registry config <name>`) uses REST API endpoint `/api/v1/registry/config/registry/{name}` (GET) and prints the full JSON response
- Registry add/update commands (`jh registry config add` / `jh registry config update`) use REST API endpoint `/api/v1/registry/config/registry/{name}` (POST); the backend creates or updates based on whether the registry already exists
- Both commands accept the full registry JSON payload via `--file <path>` or stdin; the payload `name` field identifies the registry
- Registry add/update always poll `/api/v1/registry/config/registry/{name}/savestatus` every 3 seconds up to a 2-minute timeout
- Registry registrator command (`jh registry registrator <name>`) uses REST API endpoint `/api/v1/registry/config/registrator/{name}` (GET) and prints the full JSON response
- Registry registrator update command (`jh registry registrator update <name>`) uses REST API endpoint `/api/v1/registry/config/registrator/{name}` (POST); the registry name comes from the positional argument (`RegistratorInfo` has no `name` field)
- Registrator update validates that `"email"` is non-empty when `"enabled"` is true
- GET returns 404 "Registry not found" when no registrator has been configured for that registry yet
- Bundle provider type automatically sets `license_detect: false` in the payload
- Admin token list command (`jh admin token list`) uses REST API endpoint `/app/token/activelist` which requires appropriate permissions
- Token list output is concise by default (Subject, Created By, and Expired status only); use `--verbose` flag for detailed information (signature, creation date, expiration date with estimate indicator)
- Token dates are formatted in human-readable format and converted to local timezone (respects system timezone or TZ environment variable)
- Token expiration estimate indicator only shown when `expires_at_is_estimate` is true in API response
- Registry credential commands do not accept a `--server` flag; server is always read from `~/.juliahub` config
- Credential add/update commands accept JSON as a positional argument or from stdin (pass `-` or omit argument to read stdin)
- SSH and GitHub App private keys can be supplied inline (`private_key`, raw PEM) or via file path (`private_key_file`); both are base64-encoded into a `data:application/octet-stream;base64,...` data URL before sending
- Credential list output is concise by default; use `--verbose` to show token metadata (account login, expiry, scopes, rate limit) and SSH host keys
- SSH credentials are identified by 1-based index (from `list` output) for update and delete operations
- Token and GitHub App values/private keys are omitted from update requests when not changing them (server keeps existing value)
- `rate_limit_reset` in token metadata is a Unix timestamp (int64), displayed as local time in verbose mode
- Landing page commands (`jh admin landing-page`) use REST API: GET `/app/homepage` (show), POST `/app/config/homepage` (update), DELETE `/app/config/homepage` (remove); require appropriate permissions
- Landing page `update` command accepts content inline as an argument, from a file via `--file`, or piped via stdin (priority: `--file` > arg > stdin)
- Landing page response uses custom JSON unmarshaling (`homepageResponse`) to handle `message` being either an object or a string
- Package search (`jh package search`) and info (`jh package info`) both try REST API (`/packages/info`) first, then fall back to GraphQL (`FilteredPackages` / `FilteredPackagesCount` via `/v1/graphql`) on failure; a warning is printed to stderr when the fallback is used
- REST API passes `--registries` as comma-separated registry names to the `registries` query param; GraphQL fallback passes registry IDs to the `registries` variable
- `fetchRegistries` in `registries.go` is used by `listRegistries`, `packageSearchCmd`, `packageInfoCmd`, and `packageDependencyCmd` to resolve registry names to IDs (for GraphQL) and names (for REST)
- Both REST and GraphQL package search/info paths produce identical output columns (Registry and Owner); GraphQL resolves registry names from the `registryIDs`/`registryNames` already in `PackageSearchParams` — no extra API call needed
- A package in multiple registries appears as multiple rows (one per registry) in both REST and GraphQL paths, since the GraphQL view (`package_rank_vw`) is already flattened per package-registry combination
- GraphQL fallback uses `package_search.gql` (`FilteredPackages`) for the package list and `package_search_count.gql` (`FilteredPackagesCount`) for the aggregate count as separate requests
- `executeGraphQL(server, token, req)` in `packages.go` is a shared helper for GraphQL POST requests (sets Authorization, Content-Type, Accept, X-Hasura-Role headers)
- `getPackageInfo` in `packages.go` implements exact name-match lookup using REST-first (`getPackageInfoREST`), GraphQL fallback (`getPackageInfoGraphQL`); `packageInfoCmd` in `main.go` resolves registries via `fetchRegistries`
- `getPackageDependencies` uses GraphQL (`fetchGraphQLPackages`) to locate the package, then fetches `/docs/{registry}/{package}/stable/pkg.json` for dependency data; no REST fallback (docs endpoint is authoritative)

## Implementation Details

### Registry Operations (`registries.go`)

**Shared helpers:**

- **`apiGet(url, idToken)`**: shared GET helper used by `listRegistries` and `getRegistryConfig`; retries up to 3 times on network errors or 500s, returns `[]byte` body on success
- **`readRegistryPayload(filePath)`**: reads JSON from `filePath` or stdin; validates `name` and `download_providers` are present and non-empty; returns raw `map[string]interface{}` for direct API forwarding

**`jh registry list` / `jh registry config`:**

- Both use `apiGet` for the HTTP call
- `listRegistries` unmarshals into `[]Registry` and formats output; `--verbose` adds owner, date, package count, and description
- `getRegistryConfig` pretty-prints the raw JSON response

**`jh registry config add` / `jh registry config update`:**

- Both call `submitRegistry(server, payload, operation)` with `operation` set to `"creation"` or `"update"` for status messages
- `submitRegistry` POSTs to `/api/v1/registry/config/registry/{name}` with retry on 500s, then calls `pollRegistrySaveStatus()`
- `pollRegistrySaveStatus` GETs `/api/v1/registry/config/registry/{name}/savestatus` every 3 seconds up to a 2-minute deadline

**`jh registry registrator <name>` / `jh registry registrator update`:**

- `getRegistrator` uses `apiGet` to GET `/api/v1/registry/config/registrator/{name}` and pretty-prints the JSON response
- `setRegistrator(server, name, filePath)` reads `RegistratorInfo` JSON from `--file` or stdin, validates `"email"` is set when `"enabled"` is true, then POSTs to `/api/v1/registry/config/registrator/{name}`
- No polling — the POST response is the final result

### Julia Credentials Management (`run.go`)

The Julia credentials system consists of three main functions:

1. **`createJuliaAuthFile(server, token)`**:
   - Determines depot path from `JULIA_DEPOT_PATH` environment variable (uses first path if multiple)
   - Falls back to `~/.julia` if `JULIA_DEPOT_PATH` is not set
   - Creates `{depot}/servers/<server>/auth.toml` with TOML-formatted credentials
   - Uses atomic writes: writes to temporary file, syncs, then renames
   - Includes all necessary fields: tokens, expiration, refresh URL, user info
   - Called by `setupJuliaCredentials()` and `updateJuliaCredentialsIfNeeded()`

2. **`setupJuliaCredentials()`**:
   - Public function called by:
     - `jh run` command (before starting Julia)
     - `jh run setup` command
     - `jh auth login` command (after successful login)
     - `jh auth refresh` command (after successful refresh)
   - Ensures valid token via `ensureValidToken()`
   - Creates/updates Julia auth file
   - Returns error if authentication fails

3. **`runJulia(args)`**:
   - Sets up credentials via `setupJuliaCredentials()`
   - Configures environment variables (`JULIA_PKG_SERVER`, `JULIA_PKG_USE_CLI_GIT`)
   - Executes Julia with user-provided arguments (no automatic flags)
   - Streams stdin/stdout/stderr to maintain interactive experience

### Automatic Credential Updates (`auth.go`)

The `updateJuliaCredentialsIfNeeded(server, token)` function:
- Called automatically by `ensureValidToken()` after token refresh
- Determines depot path from `JULIA_DEPOT_PATH` (same logic as `createJuliaAuthFile`)
- Checks if `{depot}/servers/<server>/auth.toml` exists
- If exists, updates it with refreshed token
- If not exists, does nothing (user hasn't used Julia integration yet)
- Errors are silently ignored to avoid breaking token operations

This ensures Julia credentials stay in sync with the main auth tokens without requiring manual intervention.

### Command Structure

- **`jh run`**: Primary command - always starts Julia after setting up credentials
- **`jh run setup`**: Subcommand - only sets up credentials without starting Julia
- **`jh auth login`**: Automatically sets up Julia credentials after successful login
- **`jh auth refresh`**: Automatically sets up Julia credentials after successful refresh