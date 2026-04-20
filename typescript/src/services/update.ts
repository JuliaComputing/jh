import { execSync, spawn } from 'child_process';
import * as os from 'os';

/**
 * Update service for CLI self-updating
 * Migrated from update.go
 */

interface GitHubRelease {
  tag_name: string;
  name: string;
  body: string;
}

export class UpdateService {
  /**
   * Get the latest release from GitHub
   */
  async getLatestRelease(): Promise<GitHubRelease> {
    const url = 'https://api.github.com/repos/JuliaComputing/jh/releases/latest';

    const resp = await fetch(url);

    if (!resp.ok) {
      throw new Error(`GitHub API returned status ${resp.status}`);
    }

    return (await resp.json()) as GitHubRelease;
  }

  /**
   * Compare two version strings
   * Returns: -1 if current < latest, 0 if equal, 1 if current > latest
   */
  compareVersions(current: string, latest: string): number {
    // Remove 'v' prefix if present
    current = current.replace(/^v/, '');
    latest = latest.replace(/^v/, '');

    // Handle "dev" version
    if (current === 'dev') {
      return -1; // Always consider dev as older
    }

    // Simple string comparison for semantic versions
    if (current === latest) {
      return 0;
    } else if (current < latest) {
      return -1;
    }
    return 1;
  }

  /**
   * Get the appropriate install script URL and command for the current platform
   */
  getInstallScript(): { url: string; command: string[]; shell: string } {
    const platform = os.platform();

    switch (platform) {
      case 'win32':
        // Check for PowerShell
        try {
          execSync('powershell -Command "exit"', { stdio: 'ignore' });
          return {
            url: 'https://raw.githubusercontent.com/JuliaComputing/jh/main/install.ps1',
            command: [
              '-ExecutionPolicy',
              'Bypass',
              '-Command',
              "Invoke-WebRequest -Uri 'https://raw.githubusercontent.com/JuliaComputing/jh/main/install.ps1' -OutFile 'install.ps1'; ./install.ps1 -NoPrompt; Remove-Item install.ps1",
            ],
            shell: 'powershell',
          };
        } catch {
          // Fallback to cmd
          return {
            url: 'https://raw.githubusercontent.com/JuliaComputing/jh/main/install.bat',
            command: [
              '/c',
              'curl -L https://raw.githubusercontent.com/JuliaComputing/jh/main/install.bat -o install.bat && install.bat && del install.bat',
            ],
            shell: 'cmd',
          };
        }

      case 'darwin':
      case 'linux':
        // Check for bash, fallback to sh
        let shell = 'bash';
        try {
          execSync('bash --version', { stdio: 'ignore' });
        } catch {
          shell = 'sh';
        }

        return {
          url: 'https://raw.githubusercontent.com/JuliaComputing/jh/main/install.sh',
          command: [
            '-c',
            `curl -sSfL https://raw.githubusercontent.com/JuliaComputing/jh/main/install.sh -o /tmp/jh_install.sh && ${shell} /tmp/jh_install.sh && rm -f /tmp/jh_install.sh`,
          ],
          shell,
        };

      default:
        throw new Error(`Unsupported platform: ${platform}`);
    }
  }

  /**
   * Run the update process
   */
  async runUpdate(currentVersion: string, force: boolean): Promise<string> {
    console.log(`Current version: ${currentVersion}`);

    // Get latest release
    const latest = await this.getLatestRelease();
    console.log(`Latest version: ${latest.tag_name}`);

    // Compare versions
    const comparison = this.compareVersions(currentVersion, latest.tag_name);

    if (comparison === 0 && !force) {
      return 'You are already running the latest version!';
    } else if (comparison > 0 && !force) {
      return `Your version (${currentVersion}) is newer than the latest release (${latest.tag_name})\nUse --force to downgrade to the latest release`;
    }

    if (comparison < 0) {
      console.log(`Update available: ${currentVersion} -> ${latest.tag_name}`);
    } else if (force) {
      console.log(`Force updating: ${currentVersion} -> ${latest.tag_name}`);
    }

    // Get install script for current platform
    const { url, command, shell } = this.getInstallScript();

    console.log(`Downloading and running install script from: ${url}`);
    console.log('This will replace the current installation...');

    // Execute the install command
    const result = spawn(shell, command, {
      stdio: 'inherit',
    });

    await new Promise<void>((resolve, reject) => {
      result.on('close', (code) => {
        if (code !== 0) {
          reject(new Error(`Update failed with code ${code}`));
        } else {
          resolve();
        }
      });
      result.on('error', reject);
    });

    return '\nUpdate completed successfully!\nYou may need to restart your terminal for the changes to take effect.';
  }
}
