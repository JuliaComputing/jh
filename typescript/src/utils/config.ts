import * as path from 'path';
import { IFileSystem } from '../types/filesystem';
import { StoredToken } from '../types/auth';

/**
 * Configuration file utilities
 * Migrated from main.go
 */

export class ConfigManager {
  constructor(private fs: IFileSystem) {}

  /**
   * Get the path to the config file (~/.juliahub)
   */
  getConfigFilePath(): string {
    return path.join(this.fs.homedir(), '.juliahub');
  }

  /**
   * Read the server from the config file
   * Returns 'juliahub.com' as default if file doesn't exist or server not found
   */
  async readConfigFile(): Promise<string> {
    const configPath = this.getConfigFilePath();

    try {
      const exists = await this.fs.exists(configPath);
      if (!exists) {
        return 'juliahub.com'; // default server
      }

      const content = await this.fs.readFile(configPath, 'utf8');
      const lines = content.split('\n');

      for (const line of lines) {
        const trimmed = line.trim();
        if (trimmed.startsWith('server=')) {
          return trimmed.substring('server='.length);
        }
      }

      return 'juliahub.com'; // default if no server line found
    } catch (error) {
      return 'juliahub.com'; // default on error
    }
  }

  /**
   * Write just the server to the config file
   */
  async writeConfigFile(server: string): Promise<void> {
    const configPath = this.getConfigFilePath();
    await this.fs.writeFile(configPath, `server=${server}\n`, { mode: 0o600 });
  }

  /**
   * Write token and server information to config file
   */
  async writeTokenToConfig(server: string, token: StoredToken): Promise<void> {
    const configPath = this.getConfigFilePath();
    let content = '';

    content += `server=${server}\n`;

    if (token.accessToken) {
      content += `access_token=${token.accessToken}\n`;
    }

    if (token.tokenType) {
      content += `token_type=${token.tokenType}\n`;
    }

    if (token.refreshToken) {
      content += `refresh_token=${token.refreshToken}\n`;
    }

    if (token.expiresIn) {
      content += `expires_in=${token.expiresIn}\n`;
    }

    if (token.idToken) {
      content += `id_token=${token.idToken}\n`;
    }

    if (token.name) {
      content += `name=${token.name}\n`;
    }

    if (token.email) {
      content += `email=${token.email}\n`;
    }

    await this.fs.writeFile(configPath, content, { mode: 0o600 });
  }

  /**
   * Read stored token from config file
   */
  async readStoredToken(): Promise<StoredToken> {
    const configPath = this.getConfigFilePath();
    const content = await this.fs.readFile(configPath, 'utf8');
    const lines = content.split('\n');

    const token: Partial<StoredToken> = {};

    for (const line of lines) {
      const trimmed = line.trim();
      if (!trimmed) continue;

      if (trimmed.startsWith('server=')) {
        token.server = trimmed.substring('server='.length);
      } else if (trimmed.startsWith('access_token=')) {
        token.accessToken = trimmed.substring('access_token='.length);
      } else if (trimmed.startsWith('refresh_token=')) {
        token.refreshToken = trimmed.substring('refresh_token='.length);
      } else if (trimmed.startsWith('token_type=')) {
        token.tokenType = trimmed.substring('token_type='.length);
      } else if (trimmed.startsWith('id_token=')) {
        token.idToken = trimmed.substring('id_token='.length);
      } else if (trimmed.startsWith('expires_in=')) {
        token.expiresIn = parseInt(trimmed.substring('expires_in='.length), 10);
      } else if (trimmed.startsWith('name=')) {
        token.name = trimmed.substring('name='.length);
      } else if (trimmed.startsWith('email=')) {
        token.email = trimmed.substring('email='.length);
      }
    }

    if (!token.accessToken) {
      throw new Error('No access token found in config');
    }

    return token as StoredToken;
  }

  /**
   * Normalize server name (add .juliahub.com suffix if needed)
   */
  normalizeServer(server: string): string {
    if (server.endsWith('.com') || server.endsWith('.dev')) {
      return server;
    }
    return `${server}.juliahub.com`;
  }
}
