import { execSync, spawn } from 'child_process';
import { AuthService } from './auth';
import { ConfigManager } from '../utils/config';
import { IFileSystem } from '../types/filesystem';
import * as path from 'path';
import * as os from 'os';

/**
 * Julia service for Julia installation and execution
 * Migrated from julia.go and run.go
 */
export class JuliaService {
  private authService: AuthService;
  private configManager: ConfigManager;

  constructor(private fs: IFileSystem) {
    this.authService = new AuthService(fs);
    this.configManager = new ConfigManager(fs);
  }

  /**
   * Check if Julia is installed
   */
  checkJuliaInstalled(): { installed: boolean; version: string } {
    try {
      const output = execSync('julia --version', { encoding: 'utf8' });
      return {
        installed: true,
        version: output.trim(),
      };
    } catch (error) {
      return {
        installed: false,
        version: '',
      };
    }
  }

  /**
   * Install Julia
   */
  async installJulia(): Promise<void> {
    const platform = os.platform();

    switch (platform) {
      case 'win32':
        await this.installJuliaWindows();
        break;
      case 'linux':
      case 'darwin':
        await this.installJuliaUnix();
        break;
      default:
        throw new Error(`Unsupported operating system: ${platform}`);
    }
  }

  /**
   * Install Julia on Windows using winget
   */
  private async installJuliaWindows(): Promise<void> {
    console.log('Installing Julia on Windows using winget...');

    const result = spawn(
      'winget',
      ['install', '--name', 'Julia', '--id', '9NJNWW8PVKMN', '-e', '-s', 'msstore'],
      { stdio: 'inherit' }
    );

    await new Promise<void>((resolve, reject) => {
      result.on('close', (code) => {
        if (code !== 0) {
          reject(new Error(`Julia installation failed with code ${code}`));
        } else {
          resolve();
        }
      });
      result.on('error', reject);
    });
  }

  /**
   * Install Julia on Unix systems using official installer
   */
  private async installJuliaUnix(): Promise<void> {
    console.log('Installing Julia using the official installer...');

    try {
      // Download the installer script
      const curlOutput = execSync('curl -fsSL https://install.julialang.org', {
        encoding: 'utf8',
      });

      // Execute the installer
      const result = spawn('sh', ['-c', 'sh -- -y --default-channel stable'], {
        stdio: ['pipe', 'inherit', 'inherit'],
      });

      // Pipe the installer script to stdin
      result.stdin?.write(curlOutput);
      result.stdin?.end();

      await new Promise<void>((resolve, reject) => {
        result.on('close', (code) => {
          if (code !== 0) {
            reject(new Error(`Julia installation failed with code ${code}`));
          } else {
            resolve();
          }
        });
        result.on('error', reject);
      });
    } catch (error) {
      throw new Error(`Failed to download Julia installer: ${error}`);
    }
  }

  /**
   * Julia install command handler
   */
  async juliaInstallCommand(): Promise<string> {
    const { installed, version } = this.checkJuliaInstalled();

    if (installed) {
      return `Julia already installed: ${version}`;
    }

    console.log('Julia not found in PATH. Installing...');
    await this.installJulia();
    return 'Julia installed successfully';
  }

  /**
   * Create Julia auth file
   */
  private async createJuliaAuthFile(server: string): Promise<void> {
    const token = await this.authService.ensureValidToken();

    // Create ~/.julia/servers/{server}/ directory
    const serverDir = path.join(this.fs.homedir(), '.julia', 'servers', server);
    await this.fs.mkdir(serverDir, { recursive: true, mode: 0o755 });

    // Parse token to get expiration time
    const claims = this.authService.decodeJWT(token.idToken);

    // Calculate refresh URL
    let authServer: string;
    if (server === 'juliahub.com') {
      authServer = 'auth.juliahub.com';
    } else {
      authServer = server;
    }
    const refreshURL = `https://${authServer}/dex/token`;

    // Write TOML content
    const content = `expires_at = ${claims.exp}
id_token = "${token.idToken}"
access_token = "${token.accessToken}"
refresh_token = "${token.refreshToken}"
refresh_url = "${refreshURL}"
expires_in = ${token.expiresIn}
user_email = "${token.email}"
expires = ${claims.exp}
user_name = "${claims.preferred_username}"
name = "${token.name}"
`;

    // Use atomic write: write to temp file, then rename
    const authFilePath = path.join(serverDir, 'auth.toml');
    const tempFile = path.join(serverDir, `.auth.toml.tmp.${Date.now()}`);

    try {
      // Write content to temp file
      await this.fs.writeFile(tempFile, content, { mode: 0o600 });

      // Atomically rename temp file to final location
      await this.fs.rename(tempFile, authFilePath);
    } catch (error) {
      // Clean up on error
      try {
        if (await this.fs.exists(tempFile)) {
          await this.fs.unlink(tempFile);
        }
      } catch {
        // Ignore cleanup errors
      }
      throw error;
    }
  }

  /**
   * Setup Julia credentials
   */
  async setupJuliaCredentials(): Promise<void> {
    // Read server configuration
    const server = await this.configManager.readConfigFile();

    // Get valid token
    await this.authService.ensureValidToken();

    // Create Julia auth file
    await this.createJuliaAuthFile(server);
  }

  /**
   * Update Julia credentials if needed
   * Called after token refresh
   */
  async updateJuliaCredentialsIfNeeded(server: string): Promise<void> {
    // Check if the auth.toml file exists
    const authFilePath = path.join(
      this.fs.homedir(),
      '.julia',
      'servers',
      server,
      'auth.toml'
    );

    try {
      const exists = await this.fs.exists(authFilePath);
      if (!exists) {
        // File doesn't exist, so user hasn't used Julia integration yet
        return;
      }

      // File exists, update it
      await this.createJuliaAuthFile(server);
    } catch (error) {
      // Silently ignore errors to avoid breaking token operations
    }
  }

  /**
   * Run Julia with JuliaHub configuration
   */
  async runJulia(args: string[]): Promise<void> {
    // Setup Julia credentials
    await this.setupJuliaCredentials();

    // Read server for environment setup
    const server = await this.configManager.readConfigFile();

    // Check if Julia is available
    const { installed } = this.checkJuliaInstalled();
    if (!installed) {
      throw new Error(
        "Julia not found in PATH. Please install Julia first using 'jh julia install'"
      );
    }

    // Set up environment variables
    const env = {
      ...process.env,
      JULIA_PKG_SERVER: `https://${server}`,
      JULIA_PKG_USE_CLI_GIT: 'true',
    };

    // Execute Julia with user-provided arguments
    const result = spawn('julia', args, {
      env,
      stdio: 'inherit',
    });

    await new Promise<void>((resolve, reject) => {
      result.on('close', (code) => {
        if (code !== 0) {
          reject(new Error(`Julia exited with code ${code}`));
        } else {
          resolve();
        }
      });
      result.on('error', reject);
    });
  }
}
