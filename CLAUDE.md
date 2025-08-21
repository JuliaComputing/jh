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
- Cross-platform installation (Windows via winget, Unix via official installer)
- Authentication file creation (`~/.julia/servers/<server>/auth.toml`)
- Package server configuration (`JULIA_PKG_SERVER`)
- Project activation (`--project=.`)

## Development Notes

- All ID fields in GraphQL responses should be typed correctly (string for UUIDs, int64 for user IDs)
- GraphQL queries are embedded as strings (consider external .gql files for complex queries)
- Error handling includes both HTTP and GraphQL error responses
- Token refresh is automatic via `ensureValidToken()`
- File uploads use multipart form data with proper content types
- Julia auth files use TOML format with `preferred_username` from JWT claims
- Git commands use `http.extraHeader` for authentication and pass through all arguments
- Git credential helper provides seamless authentication for standard Git commands
- Multi-server authentication handled automatically via credential helper
- Project filtering supports `--user` parameter for showing specific user's projects or own projects
- Clone command automatically resolves `username/project` format to project UUIDs
- Folder naming conflicts are resolved with automatic numbering (project-1, project-2, etc.)
- Credential helper follows Git protocol: responds only to JuliaHub URLs, ignores others