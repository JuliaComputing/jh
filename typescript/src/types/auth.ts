/**
 * Auth-related types and interfaces
 * Migrated from auth.go
 */

export interface DeviceCodeResponse {
  device_code: string;
  user_code: string;
  verification_uri: string;
  verification_uri_complete: string;
  expires_in: number;
  interval: number;
}

export interface TokenResponse {
  access_token: string;
  token_type: string;
  refresh_token: string;
  expires_in: number;
  id_token: string;
  error?: string;
}

export interface JWTClaims {
  iat: number; // issued at
  exp: number; // expires at
  sub: string; // subject
  iss: string; // issuer
  aud: string; // audience
  name: string;
  email: string;
  preferred_username: string;
}

export interface StoredToken {
  accessToken: string;
  refreshToken: string;
  tokenType: string;
  expiresIn: number;
  idToken: string;
  server: string;
  name: string;
  email: string;
}
