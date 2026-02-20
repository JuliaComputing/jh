#!/usr/bin/env node

import { Command } from 'commander';
import { defaultFileSystem } from './utils/node-filesystem';
import { ConfigManager } from './utils/config';
import { AuthService } from './services/auth';
import { UserService } from './services/user';
import { ProjectsService } from './services/projects';
import { DatasetsService } from './services/datasets';
import { GitService } from './services/git';
import { JuliaService } from './services/julia';
import { UpdateService } from './services/update';

// Version information (will be set during build)
const version = process.env.npm_package_version || 'dev';

// Initialize services with default filesystem
const fs = defaultFileSystem;
const configManager = new ConfigManager(fs);
const authService = new AuthService(fs);
const userService = new UserService(fs);
const projectsService = new ProjectsService(fs);
const datasetsService = new DatasetsService(fs);
const gitService = new GitService(fs);
const juliaService = new JuliaService(fs);
const updateService = new UpdateService();

// Helper to get server from flag or config
async function getServerFromFlagOrConfig(cmd: Command): Promise<string> {
  const server = cmd.opts().server;
  const serverFlagUsed = cmd.opts().server !== undefined;

  if (!serverFlagUsed) {
    const configServer = await configManager.readConfigFile();
    return configManager.normalizeServer(configServer);
  }

  return configManager.normalizeServer(server);
}

// Main CLI program
const program = new Command();

program
  .name('jh')
  .description('JuliaHub CLI - A command line interface for interacting with JuliaHub')
  .version(version);

// ==================== Auth Commands ====================

const authCmd = program
  .command('auth')
  .description('Authentication commands');

authCmd
  .command('login')
  .description('Login to JuliaHub using OAuth2 device flow')
  .option('-s, --server <server>', 'JuliaHub server', 'juliahub.com')
  .action(async (options) => {
    try {
      const server = configManager.normalizeServer(options.server);
      console.log(`Logging in to ${server}...`);

      const token = await authService.deviceFlow(server);
      const storedToken = await authService.tokenResponseToStored(server, token);

      await configManager.writeTokenToConfig(server, storedToken);
      console.log('Successfully authenticated!');

      // Setup Julia credentials
      try {
        await juliaService.setupJuliaCredentials();
      } catch (error) {
        console.log(`Warning: Failed to setup Julia credentials: ${error}`);
      }
    } catch (error) {
      console.error(`Login failed: ${error}`);
      process.exit(1);
    }
  });

authCmd
  .command('refresh')
  .description('Refresh authentication token')
  .action(async () => {
    try {
      const storedToken = await configManager.readStoredToken();

      if (!storedToken.refreshToken) {
        console.log('No refresh token found in configuration');
        process.exit(1);
      }

      console.log(`Refreshing token for server: ${storedToken.server}`);

      const refreshedToken = await authService.refreshToken(
        storedToken.server,
        storedToken.refreshToken
      );

      const newStoredToken = await authService.tokenResponseToStored(
        storedToken.server,
        refreshedToken
      );

      await configManager.writeTokenToConfig(storedToken.server, newStoredToken);
      console.log('Token refreshed successfully!');

      // Setup Julia credentials
      try {
        await juliaService.setupJuliaCredentials();
      } catch (error) {
        console.log(`Warning: Failed to setup Julia credentials: ${error}`);
      }
    } catch (error) {
      console.error(`Failed to refresh token: ${error}`);
      process.exit(1);
    }
  });

authCmd
  .command('status')
  .description('Show authentication status')
  .action(async () => {
    try {
      const storedToken = await configManager.readStoredToken();
      console.log(authService.formatTokenInfo(storedToken));
    } catch (error) {
      console.error(`Failed to read stored token: ${error}`);
      console.log("You may need to run 'jh auth login' first");
      process.exit(1);
    }
  });

authCmd
  .command('env')
  .description('Print environment variables for authentication')
  .action(async () => {
    try {
      const output = await authService.authEnvCommand();
      console.log(output);
    } catch (error) {
      console.error(`Failed to get authentication environment: ${error}`);
      process.exit(1);
    }
  });

authCmd
  .command('base64')
  .description('Print base64-encoded auth.toml to stdout')
  .action(async () => {
    try {
      const output = await authService.authBase64Command();
      console.log(output);
    } catch (error) {
      console.error(`Failed to generate base64 auth: ${error}`);
      process.exit(1);
    }
  });

// ==================== User Commands ====================

const userCmd = program.command('user').description('User information commands');

userCmd
  .command('info')
  .description('Show user information')
  .option('-s, --server <server>', 'JuliaHub server', 'juliahub.com')
  .action(async (options, cmd) => {
    try {
      const server = await getServerFromFlagOrConfig(cmd.parent as Command);
      const userInfo = await userService.getUserInfo(server);
      console.log(userService.formatUserInfo(userInfo));
    } catch (error) {
      console.error(`Failed to get user info: ${error}`);
      process.exit(1);
    }
  });

// ==================== Project Commands ====================

const projectCmd = program
  .command('project')
  .description('Project management commands');

projectCmd
  .command('list')
  .description('List projects')
  .option('-s, --server <server>', 'JuliaHub server', 'juliahub.com')
  .option('--user [username]', 'Filter projects by user')
  .action(async (options, cmd) => {
    try {
      const server = await getServerFromFlagOrConfig(cmd.parent as Command);
      const userFilter = options.user;
      const userFilterProvided = 'user' in options;

      const output = await projectsService.listProjects(
        server,
        userFilter,
        userFilterProvided
      );
      console.log(output);
    } catch (error) {
      console.error(`Failed to list projects: ${error}`);
      process.exit(1);
    }
  });

// ==================== Dataset Commands ====================

const datasetCmd = program
  .command('dataset')
  .description('Dataset management commands');

datasetCmd
  .command('list')
  .description('List datasets')
  .option('-s, --server <server>', 'JuliaHub server', 'juliahub.com')
  .action(async (options, cmd) => {
    try {
      const server = await getServerFromFlagOrConfig(cmd.parent as Command);
      const output = await datasetsService.listDatasets(server);
      console.log(output);
    } catch (error) {
      console.error(`Failed to list datasets: ${error}`);
      process.exit(1);
    }
  });

datasetCmd
  .command('download <dataset-identifier> [version] [local-path]')
  .description('Download a dataset')
  .option('-s, --server <server>', 'JuliaHub server', 'juliahub.com')
  .action(async (datasetIdentifier, versionArg, localPathArg, options, cmd) => {
    try {
      const server = await getServerFromFlagOrConfig(cmd.parent as Command);

      let version = '';
      let localPath = '';

      // Parse arguments
      if (versionArg && versionArg.startsWith('v')) {
        version = versionArg;
        localPath = localPathArg || '';
      } else if (versionArg) {
        localPath = versionArg;
      }

      await datasetsService.downloadDataset(server, datasetIdentifier, version, localPath);
    } catch (error) {
      console.error(`Failed to download dataset: ${error}`);
      process.exit(1);
    }
  });

datasetCmd
  .command('upload [dataset-identifier] <file-path>')
  .description('Upload a dataset')
  .option('-s, --server <server>', 'JuliaHub server', 'juliahub.com')
  .option('--new', 'Create a new dataset')
  .action(async (datasetIdentifierArg, filePathArg, options, cmd) => {
    try {
      const server = await getServerFromFlagOrConfig(cmd.parent as Command);
      const isNew = options.new || false;

      let datasetIdentifier = '';
      let filePath = '';

      if (filePathArg) {
        // Two arguments provided
        datasetIdentifier = datasetIdentifierArg;
        filePath = filePathArg;

        if (isNew) {
          console.error('Error: --new flag cannot be used with dataset identifier');
          process.exit(1);
        }
      } else {
        // One argument provided
        filePath = datasetIdentifierArg;

        if (!isNew) {
          console.error('Error: --new flag is required when no dataset identifier is provided');
          process.exit(1);
        }
      }

      await datasetsService.uploadDataset(server, datasetIdentifier, filePath, isNew);
    } catch (error) {
      console.error(`Failed to upload dataset: ${error}`);
      process.exit(1);
    }
  });

datasetCmd
  .command('status <dataset-identifier> [version]')
  .description('Show dataset status')
  .option('-s, --server <server>', 'JuliaHub server', 'juliahub.com')
  .action(async (datasetIdentifier, version, options, cmd) => {
    try {
      const server = await getServerFromFlagOrConfig(cmd.parent as Command);
      const output = await datasetsService.statusDataset(
        server,
        datasetIdentifier,
        version || ''
      );
      console.log(output);
    } catch (error) {
      console.error(`Failed to get dataset status: ${error}`);
      process.exit(1);
    }
  });

// ==================== Git Commands ====================

program
  .command('clone <username/project> [local-path]')
  .description('Clone a project from JuliaHub')
  .option('-s, --server <server>', 'JuliaHub server', 'juliahub.com')
  .action(async (projectIdentifier, localPath, options, cmd) => {
    try {
      const server = await getServerFromFlagOrConfig(cmd);
      await gitService.cloneProject(server, projectIdentifier, localPath);
    } catch (error) {
      console.error(`Failed to clone project: ${error}`);
      process.exit(1);
    }
  });

program
  .command('push [args...]')
  .description('Push to JuliaHub using Git with authentication')
  .option('-s, --server <server>', 'JuliaHub server', 'juliahub.com')
  .action(async (args, options, cmd) => {
    try {
      const server = await getServerFromFlagOrConfig(cmd);
      await gitService.pushProject(server, args);
    } catch (error) {
      console.error(`Failed to push: ${error}`);
      process.exit(1);
    }
  });

program
  .command('fetch [args...]')
  .description('Fetch from JuliaHub using Git with authentication')
  .option('-s, --server <server>', 'JuliaHub server', 'juliahub.com')
  .action(async (args, options, cmd) => {
    try {
      const server = await getServerFromFlagOrConfig(cmd);
      await gitService.fetchProject(server, args);
    } catch (error) {
      console.error(`Failed to fetch: ${error}`);
      process.exit(1);
    }
  });

program
  .command('pull [args...]')
  .description('Pull from JuliaHub using Git with authentication')
  .option('-s, --server <server>', 'JuliaHub server', 'juliahub.com')
  .action(async (args, options, cmd) => {
    try {
      const server = await getServerFromFlagOrConfig(cmd);
      await gitService.pullProject(server, args);
    } catch (error) {
      console.error(`Failed to pull: ${error}`);
      process.exit(1);
    }
  });

const gitCredentialCmd = program
  .command('git-credential')
  .description('Git credential helper commands');

gitCredentialCmd
  .command('get')
  .description('Get credentials for Git (internal use)')
  .action(async () => {
    try {
      await gitService.gitCredentialHelper('get');
    } catch (error) {
      console.error(`Git credential helper failed: ${error}`);
      process.exit(1);
    }
  });

gitCredentialCmd
  .command('store')
  .description('Store credentials for Git (internal use)')
  .action(async () => {
    try {
      await gitService.gitCredentialHelper('store');
    } catch (error) {
      console.error(`Git credential helper failed: ${error}`);
      process.exit(1);
    }
  });

gitCredentialCmd
  .command('erase')
  .description('Erase credentials for Git (internal use)')
  .action(async () => {
    try {
      await gitService.gitCredentialHelper('erase');
    } catch (error) {
      console.error(`Git credential helper failed: ${error}`);
      process.exit(1);
    }
  });

gitCredentialCmd
  .command('setup')
  .description('Setup git credential helper for JuliaHub')
  .action(async () => {
    try {
      await gitService.gitCredentialSetup();
    } catch (error) {
      console.error(`Failed to setup git credential helper: ${error}`);
      process.exit(1);
    }
  });

// ==================== Julia Commands ====================

const juliaCmd = program
  .command('julia')
  .description('Julia installation and management');

juliaCmd
  .command('install')
  .description('Install Julia')
  .action(async () => {
    try {
      const output = await juliaService.juliaInstallCommand();
      console.log(output);
    } catch (error) {
      console.error(`Failed to install Julia: ${error}`);
      process.exit(1);
    }
  });

const runCmd = program
  .command('run')
  .description('Run Julia with JuliaHub configuration')
  .allowUnknownOption()
  .action(async (options, cmd) => {
    try {
      // Get all arguments after 'run'
      const args = process.argv.slice(process.argv.indexOf('run') + 1);

      // Remove '--' separator if present
      const juliaArgs = args[0] === '--' ? args.slice(1) : args;

      await juliaService.runJulia(juliaArgs);
    } catch (error) {
      console.error(`Failed to run Julia: ${error}`);
      process.exit(1);
    }
  });

runCmd
  .command('setup')
  .description('Setup JuliaHub credentials for Julia')
  .action(async () => {
    try {
      await juliaService.setupJuliaCredentials();
      console.log('Julia credentials setup complete');
    } catch (error) {
      console.error(`Failed to setup Julia credentials: ${error}`);
      process.exit(1);
    }
  });

// ==================== Update Command ====================

program
  .command('update')
  .description('Update jh to the latest version')
  .option('--force', 'Force update even if current version is newer')
  .action(async (options) => {
    try {
      const force = options.force || false;
      const output = await updateService.runUpdate(version, force);
      console.log(output);
    } catch (error) {
      console.error(`Update failed: ${error}`);
      process.exit(1);
    }
  });

// Parse command line arguments
program.parse(process.argv);
