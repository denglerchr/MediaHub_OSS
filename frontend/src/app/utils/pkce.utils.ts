/**
 * Utilities for OAuth 2.0 / OpenID Connect PKCE (RFC 7636) and CSRF State management.
 */

/**
 * Converts a byte buffer to a base64url-encoded string (RFC 7636 Section 3).
 */
export function bufferToBase64Url(buffer: ArrayBuffer | Uint8Array): string {
  const bytes = buffer instanceof Uint8Array ? buffer : new Uint8Array(buffer);
  let binary = '';
  for (let i = 0; i < bytes.byteLength; i++) {
    binary += String.fromCharCode(bytes[i]);
  }
  return btoa(binary)
    .replace(/\+/g, '-')
    .replace(/\//g, '_')
    .replace(/=+$/, '');
}

/**
 * Generates a cryptographically strong random state string to mitigate Login CSRF.
 */
export function generateRandomState(): string {
  const array = new Uint8Array(24);
  window.crypto.getRandomValues(array);
  return bufferToBase64Url(array);
}

/**
 * Generates a high-entropy PKCE code verifier (RFC 7636 Section 4.1).
 * 32 random bytes results in a 43-character base64url string.
 */
export function generateCodeVerifier(): string {
  const array = new Uint8Array(32);
  window.crypto.getRandomValues(array);
  return bufferToBase64Url(array);
}

/**
 * Derives the PKCE code challenge from the code verifier using SHA-256 (RFC 7636 Section 4.2).
 */
export async function generateCodeChallenge(codeVerifier: string): Promise<string> {
  const encoder = new TextEncoder();
  const data = encoder.encode(codeVerifier);
  const digest = await window.crypto.subtle.digest('SHA-256', data);
  return bufferToBase64Url(digest);
}
