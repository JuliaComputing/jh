import {
  DeviceCodeResponse,
  TokenResponse,
  JWTClaims,
  StoredToken,
} from '../types/auth';
import { ConfigManager } from '../utils/config';
import { IFileSystem } from '../types/filesystem';

/**
 * Authentication service
 * Migrated from auth.go
 */
export class AuthService {
  private configManager: ConfigManager;

  constructor(private fs: IFileSystem) {
    this.configManager = new ConfigManager(fs);
  }

  /**
   * Decode a JWT token and extract claims
   */
  decodeJWT(tokenString: string): JWTClaims {
    const parts = tokenString.split('.');
    if (parts.length !== 3) {
      throw new Error('Invalid JWT format');
    }

    const payload = parts[1];
    // Add padding if needed
    let paddedPayload = payload;
    const padLength = payload.length % 4;
    if (padLength === 2) {
      paddedPayload += '==';
    } else if (padLength === 3) {
      paddedPayload += '=';
    }

    const decoded = Buffer.from(paddedPayload, 'base64url').toString('utf8');
    return JSON.parse(decoded) as JWTClaims;
  }

  /**
   * Check if a token has expired
   */
  isTokenExpired(accessToken: string, expiresIn: number): boolean {
    try {
      const claims = this.decodeJWT(accessToken);

      // Check if token has expired based on JWT exp claim
      if (claims.exp > 0) {
        return Math.floor(Date.now() / 1000) >= claims.exp;
      }

      // Fallback: use issued at + expires_in if exp claim is not present
      if (claims.iat > 0 && expiresIn > 0) {
        const expiryTime = claims.iat + expiresIn;
        return Math.floor(Date.now() / 1000) >= expiryTime;
      }

      // If we can't determine expiry, assume it's expired for safety
      return true;
    } catch (error) {
      // If we can't decode the token, assume it's expired
      return true;
    }
  }

  /**
   * Perform OAuth2 device flow authentication
   */
  async deviceFlow(server: string): Promise<TokenResponse> {
    let authServer: string;
    if (server === 'juliahub.com') {
      authServer = 'auth.juliahub.com';
    } else {
      authServer = server;
    }

    const deviceCodeURL = `https://${authServer}/dex/device/code`;
    const tokenURL = `https://${authServer}/dex/token`;

    // Step 1: Request device code
    const deviceParams = new URLSearchParams({
      client_id: 'device',
      scope: 'openid email profile offline_access',
      grant_type: 'urn:ietf:params:oauth:grant-type:device_code',
    });

    const deviceResp = await fetch(deviceCodeURL, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/x-www-form-urlencoded',
      },
      body: deviceParams.toString(),
    });

    if (!deviceResp.ok) {
      const errorText = await deviceResp.text();
      throw new Error(`Failed to request device code: ${errorText}`);
    }

    const deviceData = (await deviceResp.json()) as DeviceCodeResponse;

    // Step 2: Display user instructions
    console.log(
      `Go to ${deviceData.verification_uri_complete} and authorize this device`
    );
    console.log('Waiting for authorization...');

    // Wait 15 seconds before starting to poll
    await new Promise((resolve) => setTimeout(resolve, 15000));

    // Step 3: Poll for token
    while (true) {
      await new Promise((resolve) => setTimeout(resolve, 4000));

      const tokenParams = new URLSearchParams({
        client_id: 'device',
        device_code: deviceData.device_code,
        scope: 'openiod email profile offline_access',
        grant_type: 'urn:ietf:params:oauth:grant-type:device_code',
      });

      const tokenResp = await fetch(tokenURL, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/x-www-form-urlencoded',
        },
        body: tokenParams.toString(),
      });

      const tokenData = (await tokenResp.json()) as TokenResponse;

      if (tokenData.error) {
        if (tokenData.error === 'authorization_pending') {
          continue;
        }
        throw new Error(`Authorization failed: ${tokenData.error}`);
      }

      if (tokenData.access_token) {
        if (!tokenData.refresh_token) {
          console.log(
            'Warning: No refresh token received. This may indicate an issue with the authentication provider.'
          );
          console.log(
            'Consider trying the GitHub connector instead for better token management.'
          );
        }
        return tokenData;
      }
    }
  }

  /**
   * Refresh an expired token using the refresh token
   */
  async refreshToken(server: string, refreshToken: string): Promise<TokenResponse> {
    let authServer: string;
    if (server === 'juliahub.com') {
      authServer = 'auth.juliahub.com';
    } else {
      authServer = server;
    }

    const tokenURL = `https://${authServer}/dex/token`;

    const params = new URLSearchParams({
      client_id: 'device',
      grant_type: 'refresh_token',
      refresh_token: refreshToken,
    });

    const resp = await fetch(tokenURL, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/x-www-form-urlencoded',
      },
      body: params.toString(),
    });

    if (!resp.ok) {
      const errorText = await resp.text();
      throw new Error(`Failed to refresh token: ${errorText}`);
    }

    const tokenData = (await resp.json()) as TokenResponse;

    if (tokenData.error) {
      throw new Error(`Failed to refresh token: ${tokenData.error}`);
    }

    if (!tokenData.access_token) {
      throw new Error('No access token in refresh response');
    }

    return tokenData;
  }

  /**
   * Ensure we have a valid token, refreshing if necessary
   */
  async ensureValidToken(): Promise<StoredToken> {
    const storedToken = await this.configManager.readStoredToken();

    // Check if token is expired
    const expired = this.isTokenExpired(
      storedToken.accessToken,
      storedToken.expiresIn
    );

    if (!expired) {
      return storedToken;
    }

    // Token is expired, try to refresh
    if (!storedToken.refreshToken) {
      throw new Error('Access token expired and no refresh token available');
    }

    const refreshedToken = await this.refreshToken(
      storedToken.server,
      storedToken.refreshToken
    );

    // Convert TokenResponse to StoredToken
    const updatedToken: StoredToken = {
      accessToken: refreshedToken.access_token,
      refreshToken: refreshedToken.refresh_token,
      tokenType: refreshedToken.token_type,
      expiresIn: refreshedToken.expires_in,
      idToken: refreshedToken.id_token,
      server: storedToken.server,
      name: storedToken.name,
      email: storedToken.email,
    };

    // Extract name and email from new ID token if available
    if (refreshedToken.id_token) {
      try {
        const claims = this.decodeJWT(refreshedToken.id_token);
        if (claims.name) {
          updatedToken.name = claims.name;
        }
        if (claims.email) {
          updatedToken.email = claims.email;
        }
      } catch (error) {
        // Ignore errors in JWT decoding for name/email extraction
      }
    }

    // Save the refreshed token
    await this.configManager.writeTokenToConfig(storedToken.server, updatedToken);

    // Update Julia credentials if needed (we'll implement this later)
    // await this.updateJuliaCredentialsIfNeeded(storedToken.server, updatedToken);

    return updatedToken;
  }

  /**
   * Format token information for display
   */
  formatTokenInfo(token: StoredToken): string {
    try {
      const claims = this.decodeJWT(token.accessToken);
      const expired = this.isTokenExpired(token.accessToken, token.expiresIn);
      const status = expired ? 'Expired' : 'Valid';

      let result = '';
      result += `Server: ${token.server}\n`;
      result += `Token Status: ${status}\n`;
      result += `Subject: ${claims.sub}\n`;
      result += `Issuer: ${claims.iss}\n`;

      if (claims.aud) {
        result += `Audience: ${claims.aud}\n`;
      }

      if (claims.iat > 0) {
        const issuedTime = new Date(claims.iat * 1000);
        result += `Issued At: ${issuedTime.toISOString()}\n`;
      }

      if (claims.exp > 0) {
        const expireTime = new Date(claims.exp * 1000);
        result += `Expires At: ${expireTime.toISOString()}\n`;
      }

      if (token.tokenType) {
        result += `Token Type: ${token.tokenType}\n`;
      }

      result += `Has Refresh Token: ${!!token.refreshToken}\n`;

      if (token.name) {
        result += `Name: ${token.name}\n`;
      }

      if (token.email) {
        result += `Email: ${token.email}\n`;
      }

      return result;
    } catch (error) {
      return `Error decoding token: ${error}`;
    }
  }

  /**
   * Convert TokenResponse to StoredToken with server info
   */
  async tokenResponseToStored(
    server: string,
    token: TokenResponse
  ): Promise<StoredToken> {
    const storedToken: StoredToken = {
      accessToken: token.access_token,
      refreshToken: token.refresh_token,
      tokenType: token.token_type,
      expiresIn: token.expires_in,
      idToken: token.id_token,
      server: server,
      name: '',
      email: '',
    };

    // Extract name and email from ID token
    if (token.id_token) {
      try {
        const claims = this.decodeJWT(token.id_token);
        if (claims.name) {
          storedToken.name = claims.name;
        }
        if (claims.email) {
          storedToken.email = claims.email;
        }
      } catch (error) {
        // Ignore errors in JWT decoding
      }
    }

    return storedToken;
  }

  /**
   * Generate environment variables for auth
   */
  async authEnvCommand(): Promise<string> {
    const token = await this.ensureValidToken();
    const claims = this.decodeJWT(token.idToken);

    let output = '';
    output += `JULIAHUB_HOST=${token.server}\n`;
    output += `JULIAHUB_PORT=443\n`;
    output += `JULIAHUB_ID_TOKEN=${token.idToken}\n`;
    output += `JULIAHUB_ID_TOKEN_EXPIRES=${claims.exp}\n`;
    output += `\n`;
    output += `INVOCATION_HOST=${token.server}\n`;
    output += `INVOCATION_PORT=443\n`;
    output += `INVOCATION_USER_EMAIL=${token.email}\n`;

    return output;
  }

  /**
   * Generate base64-encoded auth.toml content
   */
  async authBase64Command(): Promise<string> {
    const token = await this.ensureValidToken();
    const claims = this.decodeJWT(token.idToken);

    // Calculate refresh URL
    let authServer: string;
    if (token.server === 'juliahub.com') {
      authServer = 'auth.juliahub.com';
    } else {
      authServer = token.server;
    }
    const refreshURL = `https://${authServer}/dex/token`;

    // Create auth.toml content
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

    // Encode to base64
    return Buffer.from(content, 'utf8').toString('base64');
  }
}
