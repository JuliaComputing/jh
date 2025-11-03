# JuliaHub CLI - TypeScript Implementation

This is a TypeScript migration of the Go-based JuliaHub CLI, designed to work both as a Node.js CLI tool and within browser/VSCode extension environments.

## Project Structure

```
typescript/
├── src/
│   ├── commands/          # CLI command implementations (Commander.js)
│   ├── services/          # Business logic services
│   │   ├── auth.ts        # OAuth2 device flow, token management
│   │   ├── user.ts        # User info via GraphQL
│   │   ├── projects.ts    # Project management via GraphQL
│   │   ├── datasets.ts    # Dataset operations via REST API (TODO)
│   │   ├── git.ts         # Git integration and credential helper (TODO)
│   │   └── julia.ts       # Julia installation and execution (TODO)
│   ├── types/             # TypeScript type definitions
│   │   ├── filesystem.ts  # Filesystem abstraction interface
│   │   ├── auth.ts        # Auth-related types
│   │   ├── user.ts        # User types
│   │   ├── projects.ts    # Project types
│   │   └── datasets.ts    # Dataset types
│   ├── utils/             # Utility functions
│   │   ├── node-filesystem.ts  # Node.js filesystem implementation
│   │   └── config.ts      # Config file management (~/.juliahub)
│   └── index.ts           # Main CLI entry point (TODO)
├── dist/                  # Compiled JavaScript output
├── package.json           # NPM package configuration
├── tsconfig.json          # TypeScript configuration
└── jest.config.js         # Jest testing configuration
```

## Architecture Highlights

### Filesystem Abstraction

The project uses a filesystem abstraction layer (`IFileSystem` interface) to support both Node.js and VSCode environments:

- **Node.js**: Uses `src/utils/node-filesystem.ts` (wraps `fs/promises`)
- **VSCode**: Can inject VSCode's filesystem API implementation
- **Benefits**: Same codebase works in CLI, browser, and VSCode extension

### Dependency Injection

Services accept the filesystem interface as a constructor parameter:

```typescript
const fs = new NodeFileSystem();
const authService = new AuthService(fs);
```

This allows easy testing with mock filesystems and runtime environment switching.

### Service Layer

Business logic is separated into service classes:

- `AuthService`: OAuth2 device flow, JWT decoding, token refresh
- `UserService`: GraphQL user information retrieval
- `ProjectsService`: GraphQL project listing and lookup
- (More services to be added)

### Configuration Management

The `ConfigManager` class handles ~/.juliahub file operations:
- Reading/writing server configuration
- Storing authentication tokens
- Token retrieval for API calls

## Migration Status

### ✅ Completed

- [x] TypeScript project setup (npm, TypeScript, Jest)
- [x] Filesystem abstraction interface
- [x] Config management utilities
- [x] Type definitions for auth, user, projects, datasets
- [x] AuthService (auth.go → auth.ts)
- [x] UserService (user.go → user.ts)
- [x] ProjectsService (projects.go → projects.ts)

### 🚧 In Progress

- [ ] README documentation

### 📋 TODO

- [ ] DatasetsService (datasets.go → datasets.ts)
- [ ] GitService (git.go → git.ts)
- [ ] JuliaService (julia.go + run.go → julia.ts)
- [ ] UpdateService (update.go → update.ts)
- [ ] Command implementations (Commander.js)
- [ ] Main CLI entry point (index.ts)
- [ ] Binary packaging with `pkg`
- [ ] Tests for all services
- [ ] Integration tests

## Development

### Build

```bash
npm run build         # Compile TypeScript to dist/
npm run dev           # Watch mode compilation
```

### Testing

```bash
npm test              # Run Jest tests
npm run test:watch    # Watch mode
npm run test:coverage # Coverage report
```

### Linting

```bash
npm run lint          # Type-check without emitting files
```

### Binary Packaging

```bash
npm run pkg           # Create standalone binaries for Linux, macOS, Windows
```

## API Compatibility

The TypeScript implementation maintains the same API structure as the Go version:

- **OAuth2 Device Flow**: Same endpoints and flow
- **GraphQL API**: Same queries and response structures
- **REST API**: Same dataset endpoints
- **Config File**: Same ~/.juliahub format
- **Token Management**: Same JWT structure and refresh logic

## Key Differences from Go Version

1. **Async/Await**: All I/O operations use promises (TypeScript idiomatic)
2. **Fetch API**: Uses native `fetch()` instead of `http` package
3. **Class-based Services**: OOP approach with dependency injection
4. **Type Safety**: Full TypeScript type checking
5. **Filesystem Abstraction**: Pluggable filesystem for different environments

## Usage (Once Complete)

### As CLI

```bash
# Install globally
npm install -g jh

# Or run from repo
npm run build
node dist/index.js auth login

# Or use standalone binary
./binaries/jh-linux auth login
```

### As Library (VSCode Extension)

```typescript
import { AuthService, UserService } from 'jh';
import { VSCodeFileSystem } from './vscode-filesystem';

const fs = new VSCodeFileSystem(vscode.workspace.fs);
const authService = new AuthService(fs);
const userService = new UserService(fs);

// Now services work with VSCode's filesystem
const userInfo = await userService.getUserInfo('juliahub.com');
```

## Contributing

When adding new features:

1. Create type definitions in `src/types/`
2. Implement service logic in `src/services/`
3. Create command handlers in `src/commands/`
4. Wire up commands in `src/index.ts`
5. Add tests in `__tests__/` or `.test.ts` files

## License

ISC
