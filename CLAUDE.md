# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is a Go-based CLI tool for interacting with JuliaHub, a platform for Julia computing. The CLI provides commands for authentication, dataset management, project management, user information, Git integration, and Julia integration.

## Architecture

The application follows a command-line interface pattern using the Cobra library with a modular file structure:

- **main.go**: Core CLI structure with command definitions and configuration management
- **auth.go**: OAuth2 device flow authentication with JWT token handling
- **datasets.go**: Dataset operations (list, download, upload, status) with REST API integration
- **projects.go**: Project management using GraphQL API with user filtering
- **user.go**: User information retrieval using GraphQL API
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
   - **REST API**: Used for dataset operations (`/api/v1/datasets`, `/datasets/{uuid}/url/{version}`)
   - **GraphQL API**: Used for projects and user info (`/v1/graphql`)
   - **Headers**: All GraphQL requests require `X-Hasura-Role: jhuser` header
   - **Authentication**: Uses ID tokens (`token.IDToken`) for API calls

3. **Command Structure**:
   - `jh auth`: Authentication commands (login, refresh, status, env)
   - `jh dataset`: Dataset operations (list, download, upload, status)
   - `jh project`: Project management (list with GraphQL, supports user filtering)
   - `jh user`: User information (info with GraphQL)
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

### Test project and user operations
```bash
go run . project list
go run . project list --user
go run . project list --user john
go run . user info
```

### Test Git operations
```bash
go run . clone john/my-project
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
- **Authentication**: Bearer token with ID token
- **Upload workflow**: 3-step process (request presigned URL, upload to URL, close upload)

### Data Type Handling
- Project/dataset IDs are UUID strings, not integers
- User IDs are integers
- Custom time parsing handles multiple date formats
- Flexible dataset identifier resolution (UUID, name, user/name)

## Git Integration

The CLI provides seamless Git integration with JuliaHub authentication through two approaches:

### Method 1: JuliaHub CLI Wrapper Commands
- **Clone**: `jh clone username/project` - resolves project names to UUIDs and clones with authentication
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

## Julia Integration

The CLI provides Julia installation and execution with JuliaHub configuration:

### Julia Installation (`jh julia install`)
- Cross-platform installation (Windows via winget, Unix via official installer)
- Installs latest stable Julia version

### Julia Credentials
- **Authentication file**: Automatically creates `~/.julia/servers/<server>/auth.toml`
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
- Creates/updates `~/.julia/servers/<server>/auth.toml` with current credentials
- Does not start Julia
- Useful for explicitly updating credentials

## Development Notes

- All ID fields in GraphQL responses should be typed correctly (string for UUIDs, int64 for user IDs)
- GraphQL queries are embedded as strings (consider external .gql files for complex queries)
- Error handling includes both HTTP and GraphQL error responses
- Token refresh is automatic via `ensureValidToken()`
- File uploads use multipart form data with proper content types
- Julia auth files use TOML format with `preferred_username` from JWT claims
- Julia auth files use atomic writes (temp file + rename) to prevent corruption
- Julia credentials are automatically updated after login and token refresh
- Git commands use `http.extraHeader` for authentication and pass through all arguments
- Git credential helper provides seamless authentication for standard Git commands
- Multi-server authentication handled automatically via credential helper
- Project filtering supports `--user` parameter for showing specific user's projects or own projects
- Clone command automatically resolves `username/project` format to project UUIDs
- Folder naming conflicts are resolved with automatic numbering (project-1, project-2, etc.)
- Credential helper follows Git protocol: responds only to JuliaHub URLs, ignores others

## Implementation Details

### Julia Credentials Management (`run.go`)

The Julia credentials system consists of three main functions:

1. **`createJuliaAuthFile(server, token)`**:
   - Creates `~/.julia/servers/<server>/auth.toml` with TOML-formatted credentials
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
- Checks if `~/.julia/servers/<server>/auth.toml` exists
- If exists, updates it with refreshed token
- If not exists, does nothing (user hasn't used Julia integration yet)
- Errors are silently ignored to avoid breaking token operations

This ensures Julia credentials stay in sync with the main auth tokens without requiring manual intervention.

### Command Structure

- **`jh run`**: Primary command - always starts Julia after setting up credentials
- **`jh run setup`**: Subcommand - only sets up credentials without starting Julia
- **`jh auth login`**: Automatically sets up Julia credentials after successful login
- **`jh auth refresh`**: Automatically sets up Julia credentials after successful refresh