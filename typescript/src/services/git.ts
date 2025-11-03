import { execSync, spawn } from 'child_process';
import { AuthService } from './auth';
import { ProjectsService } from './projects';
import { IFileSystem } from '../types/filesystem';
import * as path from 'path';
import * as readline from 'readline';

/**
 * Git service for Git operations with JuliaHub authentication
 * Migrated from git.go
 */
export class GitService {
  private authService: AuthService;
  private projectsService: ProjectsService;

  constructor(private fs: IFileSystem) {
    this.authService = new AuthService(fs);
    this.projectsService = new ProjectsService(fs);
  }

  /**
   * Check if git is installed
   */
  checkGitInstalled(): void {
    try {
      execSync('git --version', { stdio: 'ignore' });
    } catch (error) {
      throw new Error('git is not installed or not in PATH');
    }
  }

  /**
   * Clone a project from JuliaHub
   */
  async cloneProject(
    server: string,
    projectIdentifier: string,
    localPath?: string
  ): Promise<void> {
    this.checkGitInstalled();

    const token = await this.authService.ensureValidToken();

    // Parse the project identifier
    if (!projectIdentifier.includes('/')) {
      throw new Error("Project identifier must be in format 'username/project'");
    }

    const [username, projectName] = projectIdentifier.split('/', 2);

    // Find the project by username and project name
    const projectUUID = await this.projectsService.findProjectByUserAndName(
      server,
      username,
      projectName
    );

    // Construct the Git URL
    const gitURL = `https://${server}/git/projects/${projectUUID}`;
    const authHeader = `Authorization: Bearer ${token.idToken}`;

    console.log(`Cloning project: ${username}/${projectName}`);
    console.log(`Git URL: ${gitURL}`);

    // Prepare git clone command with authorization header
    const args = ['-c', `http.extraHeader=${authHeader}`, 'clone', gitURL];
    if (localPath) {
      args.push(localPath);
    }

    // Execute git clone
    const result = spawn('git', args, {
      stdio: 'inherit',
    });

    await new Promise<void>((resolve, reject) => {
      result.on('close', (code) => {
        if (code !== 0) {
          reject(new Error(`git clone failed with code ${code}`));
        } else {
          resolve();
        }
      });
      result.on('error', reject);
    });

    // If no local path was specified, rename the UUID folder to project name
    if (!localPath) {
      const uuidFolderPath = path.join(process.cwd(), projectUUID);
      let projectFolderPath = path.join(process.cwd(), projectName);

      // Check if the UUID folder exists
      if (await this.fs.exists(uuidFolderPath)) {
        // Check if target folder already exists
        if (await this.fs.exists(projectFolderPath)) {
          // Target folder exists, find an available name
          projectFolderPath = await this.findAvailableFolderName(projectName);
          console.log(
            `Warning: Folder '${projectName}' already exists, using '${path.basename(projectFolderPath)}' instead`
          );
        }

        // Rename the folder from UUID to project name
        await this.fs.rename(uuidFolderPath, projectFolderPath);
        console.log(
          `Renamed folder from ${projectUUID} to ${path.basename(projectFolderPath)}`
        );
      }

      console.log(
        `Successfully cloned project to ${path.basename(projectFolderPath)}`
      );
    } else {
      console.log(`Successfully cloned project to ${localPath}`);
    }
  }

  /**
   * Find an available folder name by appending a number
   */
  private async findAvailableFolderName(baseName: string): Promise<string> {
    let counter = 1;
    while (true) {
      const candidateName = path.join(process.cwd(), `${baseName}-${counter}`);
      if (!(await this.fs.exists(candidateName))) {
        return candidateName;
      }
      counter++;
    }
  }

  /**
   * Execute git push with authentication
   */
  async pushProject(server: string, args: string[]): Promise<void> {
    this.checkGitInstalled();

    const token = await this.authService.ensureValidToken();
    const authHeader = `Authorization: Bearer ${token.idToken}`;

    const gitArgs = ['-c', `http.extraHeader=${authHeader}`, 'push', ...args];

    const result = spawn('git', gitArgs, {
      stdio: 'inherit',
    });

    await new Promise<void>((resolve, reject) => {
      result.on('close', (code) => {
        if (code !== 0) {
          reject(new Error(`git push failed with code ${code}`));
        } else {
          resolve();
        }
      });
      result.on('error', reject);
    });
  }

  /**
   * Execute git fetch with authentication
   */
  async fetchProject(server: string, args: string[]): Promise<void> {
    this.checkGitInstalled();

    const token = await this.authService.ensureValidToken();
    const authHeader = `Authorization: Bearer ${token.idToken}`;

    const gitArgs = ['-c', `http.extraHeader=${authHeader}`, 'fetch', ...args];

    const result = spawn('git', gitArgs, {
      stdio: 'inherit',
    });

    await new Promise<void>((resolve, reject) => {
      result.on('close', (code) => {
        if (code !== 0) {
          reject(new Error(`git fetch failed with code ${code}`));
        } else {
          resolve();
        }
      });
      result.on('error', reject);
    });
  }

  /**
   * Execute git pull with authentication
   */
  async pullProject(server: string, args: string[]): Promise<void> {
    this.checkGitInstalled();

    const token = await this.authService.ensureValidToken();
    const authHeader = `Authorization: Bearer ${token.idToken}`;

    const gitArgs = ['-c', `http.extraHeader=${authHeader}`, 'pull', ...args];

    const result = spawn('git', gitArgs, {
      stdio: 'inherit',
    });

    await new Promise<void>((resolve, reject) => {
      result.on('close', (code) => {
        if (code !== 0) {
          reject(new Error(`git pull failed with code ${code}`));
        } else {
          resolve();
        }
      });
      result.on('error', reject);
    });
  }

  /**
   * Git credential helper implementation
   */
  async gitCredentialHelper(action: string): Promise<void> {
    switch (action) {
      case 'get':
        await this.gitCredentialGet();
        break;
      case 'store':
      case 'erase':
        // These are no-ops for JuliaHub since we manage tokens ourselves
        break;
      default:
        throw new Error(`Unknown credential helper action: ${action}`);
    }
  }

  /**
   * Handle git credential 'get' action
   */
  private async gitCredentialGet(): Promise<void> {
    // Read input from stdin
    const input = await this.readCredentialInput();

    // Check if this is a JuliaHub URL
    if (!this.isJuliaHubURL(input.host || '')) {
      // Not a JuliaHub URL, return empty (let other credential helpers handle it)
      return;
    }

    const requestedServer = input.host || '';

    // Check if we have a stored token and if the server matches
    try {
      const storedToken = await this.authService['configManager'].readStoredToken();

      if (storedToken.server !== requestedServer) {
        // Server mismatch - need to authenticate
        console.error(`JuliaHub CLI: Authenticating to ${requestedServer}...`);

        const normalizedServer = this.authService['configManager'].normalizeServer(
          requestedServer
        );

        // Perform device flow authentication
        const token = await this.authService.deviceFlow(normalizedServer);

        // Convert and save token
        const storedToken = await this.authService.tokenResponseToStored(
          normalizedServer,
          token
        );
        await this.authService['configManager'].writeTokenToConfig(
          normalizedServer,
          storedToken
        );

        console.error(`Successfully authenticated to ${requestedServer}!`);

        // Output credentials
        console.log('username=oauth2');
        console.log(`password=${token.id_token}`);
        return;
      }

      // Server matches, ensure we have a valid token
      const validToken = await this.authService.ensureValidToken();

      // Output credentials
      console.log('username=oauth2');
      console.log(`password=${validToken.idToken}`);
    } catch (error) {
      // No stored token or error - need to authenticate
      console.error(`JuliaHub CLI: Authenticating to ${requestedServer}...`);

      const normalizedServer = this.authService['configManager'].normalizeServer(
        requestedServer
      );

      const token = await this.authService.deviceFlow(normalizedServer);
      const storedToken = await this.authService.tokenResponseToStored(
        normalizedServer,
        token
      );
      await this.authService['configManager'].writeTokenToConfig(
        normalizedServer,
        storedToken
      );

      console.error(`Successfully authenticated to ${requestedServer}!`);

      console.log('username=oauth2');
      console.log(`password=${token.id_token}`);
    }
  }

  /**
   * Read credential input from stdin
   */
  private async readCredentialInput(): Promise<Record<string, string>> {
    const input: Record<string, string> = {};
    const rl = readline.createInterface({
      input: process.stdin,
      output: process.stdout,
      terminal: false,
    });

    return new Promise((resolve) => {
      rl.on('line', (line) => {
        const trimmed = line.trim();
        if (!trimmed) {
          rl.close();
          return;
        }

        const parts = trimmed.split('=', 2);
        if (parts.length === 2) {
          input[parts[0]] = parts[1];
        }
      });

      rl.on('close', () => {
        resolve(input);
      });
    });
  }

  /**
   * Check if a host is a JuliaHub server
   */
  private isJuliaHubURL(host: string): boolean {
    if (!host) {
      return false;
    }

    // Check for juliahub.com and its subdomains
    if (host.endsWith('juliahub.com')) {
      return true;
    }

    // Check for any host that might be a JuliaHub server
    if (host.includes('juliahub')) {
      return true;
    }

    // Check against configured server
    try {
      const configManager = this.authService['configManager'];
      const configServer = configManager.readConfigFile();
      // This is async but we'll handle it synchronously for now
      return false; // TODO: Make this properly async
    } catch {
      return false;
    }
  }

  /**
   * Setup git credential helper
   */
  async gitCredentialSetup(): Promise<void> {
    this.checkGitInstalled();

    // Get the path to the current executable
    const execPath = process.argv[1]; // Path to the Node script

    // Set up credential helper for JuliaHub domains
    const juliaHubDomains = ['juliahub.com', '*.juliahub.com'];

    // Also check if there's a custom server configured
    try {
      const configServer = await this.authService['configManager'].readConfigFile();
      if (configServer && configServer !== 'juliahub.com') {
        juliaHubDomains.push(configServer);
      }
    } catch {
      // Ignore errors reading config
    }

    console.log('Configuring Git credential helper for JuliaHub...');

    for (const domain of juliaHubDomains) {
      const credentialKey = `credential.https://${domain}.helper`;
      const credentialValue = `${execPath} git-credential`;

      try {
        execSync(`git config --global ${credentialKey} "${credentialValue}"`, {
          stdio: 'ignore',
        });
        console.log(`✓ Configured credential helper for ${domain}`);
      } catch (error) {
        throw new Error(`Failed to configure git credential helper for ${domain}`);
      }
    }

    console.log('\nGit credential helper setup complete!');
    console.log('\nYou can now use standard Git commands with JuliaHub repositories:');
    console.log('  git clone https://juliahub.com/git/projects/username/project.git');
    console.log('  git push');
    console.log('  git pull');
    console.log('  git fetch');
    console.log(
      '\nThe JuliaHub CLI will automatically provide authentication when needed.'
    );
  }
}
