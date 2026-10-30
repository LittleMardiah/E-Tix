import axios, { AxiosError, AxiosHeaders, InternalAxiosRequestConfig } from 'axios';

/**
 * Base URL of the Go backend.
 *
 * NOTE: routes are registered at the root of the router (no /api/v1 prefix),
 * see backend/cmd/api/main.go and docs/API CONTRACT.txt section 4.
 */
export const API_BASE_URL =
  'http://localhost:8080';

export const ACCESS_TOKEN_KEY = 'access_token';
export const REFRESH_TOKEN_KEY = 'refresh_token';

export interface RetriableRequestConfig extends InternalAxiosRequestConfig {
  _retry?: boolean;
}

export interface ApiErrorBody {
  success?: boolean;
  data?: unknown;
  error?: {
    code?: string;
    message?: string;
  };
  meta?: Record<string, unknown>;
}

/**
 * Endpoints that must never trigger the refresh flow.
 * A 401 on these endpoints means "wrong credentials / wrong OTP",
 * not "access token expired". Retrying them would wipe a valid session
 * and hide the real error message from the user.
 */
const NO_REFRESH_ENDPOINTS = [
  '/auth/login',
  '/auth/register',
  '/auth/refresh-token',
  '/auth/verify-otp',
];

export const apiClient = axios.create({
  baseURL: API_BASE_URL,
  headers: { 
    'Content-Type': 'application/json',
    'ngrok-skip-browser-warning': 'true'
  },
  timeout: 15000,
});

export function getAccessToken(): string | null {
  if (typeof window === 'undefined') return null;
  return window.localStorage.getItem(ACCESS_TOKEN_KEY);
}

export function getRefreshToken(): string | null {
  if (typeof window === 'undefined') return null;
  return window.localStorage.getItem(REFRESH_TOKEN_KEY);
}

export function setTokens(accessToken: string, refreshToken: string): void {
  if (typeof window === 'undefined') return;
  window.localStorage.setItem(ACCESS_TOKEN_KEY, accessToken);
  window.localStorage.setItem(REFRESH_TOKEN_KEY, refreshToken);
}

export function clearTokens(): void {
  if (typeof window === 'undefined') return;
  window.localStorage.removeItem(ACCESS_TOKEN_KEY);
  window.localStorage.removeItem(REFRESH_TOKEN_KEY);
}

/**
 * X-Idempotency-Key is mandatory for POST mutations (docs/API CONTRACT.txt 4.1).
 * The register endpoint rejects the request with 400 when the header is missing.
 */
export function createIdempotencyKey(): string {
  const cryptoRef =
    typeof globalThis !== 'undefined' ? globalThis.crypto : undefined;
  if (cryptoRef && typeof cryptoRef.randomUUID === 'function') {
    return cryptoRef.randomUUID();
  }
  return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, (char) => {
    const random = (Math.random() * 16) | 0;
    const value = char === 'x' ? random : (random & 0x3) | 0x8;
    return value.toString(16);
  });
}

/** Extracts the human readable message from the API error envelope. */
export function getApiErrorMessage(error: unknown, fallback: string): string {
  if (axios.isAxiosError<ApiErrorBody>(error)) {
    const message = error.response?.data?.error?.message;
    if (message) return message;
  }
  return fallback;
}

apiClient.interceptors.request.use((config) => {
  const token = getAccessToken();
  if (token) {
    config.headers = AxiosHeaders.from(config.headers);
    config.headers.set('Authorization', `Bearer ${token}`);
  }
  return config;
});

let refreshRequest: Promise<string> | null = null;

/**
 * Single flight refresh: the backend rotates refresh tokens and invalidates
 * the previous one, so parallel 401s must share a single refresh call.
 */
function requestNewAccessToken(): Promise<string> {
  if (!refreshRequest) {
    refreshRequest = axios
      .post<{ data: { access_token: string; refresh_token: string } }>(
        `${API_BASE_URL}/auth/refresh-token`,
        { refresh_token: getRefreshToken() },
        { headers: { 'Content-Type': 'application/json',
    'ngrok-skip-browser-warning': 'true' }, timeout: 15000 }
      )
      .then((response) => {
        const { access_token, refresh_token } = response.data.data;
        setTokens(access_token, refresh_token);
        return access_token;
      })
      .finally(() => {
        refreshRequest = null;
      });
  }
  return refreshRequest;
}

apiClient.interceptors.response.use(
  (response) => response,
  async (error: AxiosError) => {
    const originalRequest = error.config as RetriableRequestConfig | undefined;
    const status = error.response?.status;
    const url = originalRequest?.url ?? '';

    const isAuthEndpoint = NO_REFRESH_ENDPOINTS.some((path) =>
      url.includes(path)
    );

    if (status !== 401 || !originalRequest || originalRequest._retry) {
      return Promise.reject(error);
    }

    if (isAuthEndpoint || !getRefreshToken()) {
      return Promise.reject(error);
    }

    originalRequest._retry = true;

    originalRequest.headers = AxiosHeaders.from(originalRequest.headers);

    /**
     * Another request may already have refreshed the session while this 401
     * was in flight. Reuse the fresh token instead of burning a second
     * refresh call (refresh tokens rotate, the previous one is invalidated).
     */
    const tokenUsedByRequest = originalRequest.headers.get('Authorization');
    const currentToken = getAccessToken();
    if (currentToken && tokenUsedByRequest !== `Bearer ${currentToken}`) {
      originalRequest.headers.set('Authorization', `Bearer ${currentToken}`);
      return apiClient(originalRequest);
    }

    try {
      const accessToken = await requestNewAccessToken();
      originalRequest.headers.set('Authorization', `Bearer ${accessToken}`);
      return apiClient(originalRequest);
    } catch (refreshError) {
      clearTokens();
      if (
        typeof window !== 'undefined' &&
        !window.location.pathname.startsWith('/auth/login')
      ) {
        window.location.href = '/auth/login';
      }
      return Promise.reject(refreshError);
    }
  }
);
