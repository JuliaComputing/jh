# JuliaHub CLI (`jh`)

A command-line interface for interacting with JuliaHub, a platform for Julia computing that provides dataset management, job execution, project management, Git integration, and package hosting capabilities.

## Features

- **Authentication**: OAuth2 device flow authentication with JWT token handling
- **Dataset Management**: List, download, upload, and check status of datasets
- **Project Management**: List and filter projects using GraphQL API
- **Git Integration**: Clone, push, fetch, and pull with automatic JuliaHub authentication
- **Julia Integration**: Install Julia and run with JuliaHub package server configuration
- **User Management**: Display user information and profile details

## Installation

### Quick Install (Recommended)

Install the latest release automatically:

```bash
curl -sSfL https://raw.githubusercontent.com/JuliaComputing/gojuliahub/main/install.sh | sh
```

Or download and run the script manually:

```bash
wget https://raw.githubusercontent.com/JuliaComputing/gojuliahub/main/install.sh
chmod +x install.sh
./install.sh
```

**Options:**
- `--install-dir DIR`: Custom installation directory (default: `$HOME/.local/bin`)
- `--help`: Show help message

**Custom installation directory example:**
```bash
curl -sSfL https://raw.githubusercontent.com/JuliaComputing/gojuliahub/main/install.sh | sh -s -- --install-dir /usr/local/bin
```

### Download Binary Manually

Download the latest release from the [GitHub releases page](https://github.com/JuliaComputing/gojuliahub/releases).

Available for:
- Linux (amd64, arm64)
- macOS (amd64, arm64)
- Windows (amd64, arm64)

### Build from Source

```bash
git clone https://github.com/JuliaComputing/gojuliahub
cd gojuliahub
go build -o jh .
```

## Quick Start

1. **Authenticate with JuliaHub:**

   ```bash
   jh auth login
   ```

2. **List your datasets:**

   ```bash
   jh dataset list
   ```

3. **List your projects:**

   ```bash
   jh project list
   ```

4. **Clone a project:**

   ```bash
   jh clone username/project-name
   ```

5. **Setup Git credential helper** (recommended for seamless Git operations):
   ```bash
   jh git-credential setup
   # Now you can use standard Git commands with JuliaHub repositories
   git clone https://juliahub.com/git/projects/uuid.git
   ```

## Commands

### Authentication (`jh auth`)

- `jh auth login` - Login to JuliaHub using OAuth2 device flow
- `jh auth refresh` - Refresh authentication token
- `jh auth status` - Show authentication status
- `jh auth env` - Print environment variables for authentication

### Dataset Management (`jh dataset`)

- `jh dataset list` - List all accessible datasets
- `jh dataset download <dataset-id> [version] [local-path]` - Download a dataset
- `jh dataset upload [dataset-id] <file-path>` - Upload a dataset
- `jh dataset status <dataset-id> [version]` - Show dataset status

### Project Management (`jh project`)

- `jh project list` - List all accessible projects
- `jh project list --user` - List only your projects
- `jh project list --user <username>` - List specific user's projects

### Git Credential Helper (Recommended)

- `jh git-credential setup` - Configure Git to use JuliaHub authentication
- After setup, use standard Git commands: `git clone`, `git push`, `git pull`

### Git Operations

- `jh clone <username/project> [local-path]` - Clone a JuliaHub project by username/project name
- `jh push [git-args...]` - Push with authentication
- `jh fetch [git-args...]` - Fetch with authentication
- `jh pull [git-args...]` - Pull with authentication

### Julia Integration

- `jh julia install` - Install Julia programming language
- `jh run` - Start Julia with JuliaHub configuration

### User Information (`jh user`)

- `jh user info` - Show detailed user information

## Configuration

Configuration is stored in `~/.juliahub` with 0600 permissions. The file contains:

- Server configuration
- Authentication tokens (access, refresh, ID tokens)
- User information (name, email)

Default server: `juliahub.com`

Currently, you will only be logged in one server at a time.

## Examples

### Dataset Operations

```bash
# List datasets
jh dataset list

# Download latest version of a dataset
jh dataset download my-dataset

# Download specific version
jh dataset download username/dataset-name v2

# Upload new dataset
jh dataset upload --new ./my-data.tar.gz

# Upload new version to existing dataset
jh dataset upload my-dataset ./updated-data.tar.gz
```

### Project Operations

```bash
# List all projects you have access to
jh project list

# List only your projects
jh project list --user

# List projects by specific user
jh project list --user alice
```

### Git Workflow

```bash
# Option 1: Use JuliaHub CLI wrapper commands (resolves uuids for you)
jh clone alice/my-project
cd my-project
# Make changes...
jh push

# Option 2: Use Git credential helper (recommended)
jh git-credential setup
git clone https://juliahub.com/git/projects/uuid
cd my-project
# Make changes...
git push
```

Note: It's recommended to use the git-credential helper, but you can still
clone using `jh clone username/project-name`; otherwise you need the project's uuid

## Architecture

- **Built with Go** using the Cobra CLI framework
- **Authentication**: OAuth2 device flow with JWT token management
- **APIs**: REST API for datasets, GraphQL API for projects and user info
- **Git Integration**: Seamless authentication via HTTP headers or credential helper
- **Cross-platform**: Supports Windows, macOS, and Linux

## Development

### Build and Test

```bash
# Run tests
go test ./...

# Build
go build -o jh .

# Code quality checks
go fmt ./...
go vet ./...
```

### Contributing

1. Fork the repository
2. Create a feature branch
3. Make changes with tests
4. Run `go fmt ./...` and `go vet ./...`
5. Submit a pull request

### Releasing

To create a new release:

1. Create and push a version tag:

   ```bash
   git tag v1.0.0
   git push origin v1.0.0
   ```

2. GitHub Actions will automatically:

   - Build binaries for all platforms
   - Create a GitHub release with the binaries
   - Include version information in the binaries

3. Version information is embedded in binaries:
   ```bash
   jh --version  # Shows version, commit, and build date
   ```
