import { generateRandomState, generateCodeVerifier, generateCodeChallenge, bufferToBase64Url } from './pkce.utils';

describe('PKCE and State Utilities', () => {
  it('should generate non-empty unique random states', () => {
    const state1 = generateRandomState();
    const state2 = generateRandomState();

    expect(state1).toBeTruthy();
    expect(state2).toBeTruthy();
    expect(state1).not.toEqual(state2);
    // 24 bytes in base64url is 32 characters
    expect(state1.length).toBe(32);
    expect(state1).toMatch(/^[A-Za-z0-9_-]+$/);
  });

  it('should generate valid code verifier of 43 characters', () => {
    const verifier1 = generateCodeVerifier();
    const verifier2 = generateCodeVerifier();

    expect(verifier1).toBeTruthy();
    expect(verifier2).toBeTruthy();
    expect(verifier1).not.toEqual(verifier2);
    // 32 bytes in base64url without padding is 43 characters
    expect(verifier1.length).toBe(43);
    expect(verifier1).toMatch(/^[A-Za-z0-9_-]+$/);
  });

  it('should derive code challenge matching RFC 7636 SHA-256 specification', async () => {
    // RFC 7636 Appendix B test vector:
    // Code verifier: "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
    // Expected challenge: "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
    const rfcVerifier = 'dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk';
    const expectedChallenge = 'E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM';

    const challenge = await generateCodeChallenge(rfcVerifier);
    expect(challenge).toEqual(expectedChallenge);
  });

  it('bufferToBase64Url should properly encode binary buffers without padding', () => {
    const bytes = new Uint8Array([251, 255, 254]);
    const result = bufferToBase64Url(bytes);
    expect(result).not.toContain('+');
    expect(result).not.toContain('/');
    expect(result).not.toContain('=');
    expect(result).toBe('-__-');
  });
});
