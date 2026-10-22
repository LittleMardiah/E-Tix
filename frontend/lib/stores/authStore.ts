import { create } from 'zustand';
import {
  apiClient,
  clearTokens,
  createIdempotencyKey,
  getAccessToken,
  getApiErrorMessage,
  getRefreshToken,
  setTokens,
} from '../api/client';

export interface AuthUser {
  user_id: string;
  email: string;
  name: string;
  role: string;
  tier?: string;
}

export interface LoginPayload {
  email: string;
  password: string;
}

export interface RegisterPayload {
  name: string;
  email: string;
  phone: string;
  password: string;
}

/**
 * Session state that must survive a full page reload between the register
 * screen and the OTP screen. The OTP endpoint needs the freshly issued
 * access token (Bearer) and the user_id that must match the token subject.
 */
export interface PendingVerification {
  user_id: string;
  email: string;
}

export const PENDING_VERIFICATION_KEY = 'pending_verification';

export function savePendingVerification(payload: PendingVerification): void {
  if (typeof window === 'undefined') return;
  window.localStorage.setItem(
    PENDING_VERIFICATION_KEY,
    JSON.stringify(payload)
  );
}

export function readPendingVerification(): PendingVerification | null {
  if (typeof window === 'undefined') return null;
  const raw = window.localStorage.getItem(PENDING_VERIFICATION_KEY);
  if (!raw) return null;
  try {
    return JSON.parse(raw) as PendingVerification;
  } catch {
    return null;
  }
}

export function clearPendingVerification(): void {
  if (typeof window === 'undefined') return;
  window.localStorage.removeItem(PENDING_VERIFICATION_KEY);
}

interface AuthState {
  user: AuthUser | null;
  accessToken: string | null;
  isLoading: boolean;
  error: string | null;
  login: (payload: LoginPayload) => Promise<void>;
  register: (payload: RegisterPayload) => Promise<void>;
  logout: () => void;
  setUser: (user: AuthUser) => void;
  clearError: () => void;
}

export const useAuthStore = create<AuthState>((set) => ({
  user: null,
  accessToken: null,
  isLoading: false,
  error: null,

  login: async ({ email, password }) => {
    set({ isLoading: true, error: null });
    try {
      const response = await apiClient.post('/auth/login', { email, password });
      const data = response.data.data as {
        user_id: string;
        email: string;
        name: string;
        role: string;
        tier?: string;
        access_token: string;
        refresh_token: string;
      };

      setTokens(data.access_token, data.refresh_token);

      const user: AuthUser = {
        user_id: data.user_id,
        email: data.email,
        name: data.name,
        role: data.role,
        tier: data.tier,
      };

      set({
        user,
        accessToken: data.access_token,
        isLoading: false,
        error: null,
      });
    } catch (error) {
      set({
        isLoading: false,
        error: getApiErrorMessage(error, 'Login failed. Please try again.'),
      });
      throw error;
    }
  },

  register: async ({ name, email, phone, password }) => {
    set({ isLoading: true, error: null });
    try {
      const response = await apiClient.post(
        '/auth/register',
        { name, email, phone, password, role: 'customer' },
        { headers: { 'X-Idempotency-Key': createIdempotencyKey() } }
      );
      const data = response.data.data as {
        user_id: string;
        access_token: string;
        refresh_token: string;
      };

      setTokens(data.access_token, data.refresh_token);
      savePendingVerification({ user_id: data.user_id, email });

      set({
        user: {
          user_id: data.user_id,
          email,
          name,
          role: 'customer',
        },
        accessToken: data.access_token,
        isLoading: false,
        error: null,
      });
    } catch (error) {
      set({
        isLoading: false,
        error: getApiErrorMessage(
          error,
          'Registration failed. Please try again.'
        ),
      });
      throw error;
    }
  },

  logout: () => {
    const refreshToken = getRefreshToken();
    const accessToken = getAccessToken();
    clearTokens();
    clearPendingVerification();
    set({ user: null, accessToken: null, error: null });
    if (refreshToken) {
      /**
       * The endpoint sits behind AuthMiddleware, so the access token has to
       * travel with the request. The session is cleared first, therefore the
       * header is passed explicitly instead of relying on the interceptor.
       */
      apiClient
        .post(
          '/auth/logout',
          { refresh_token: refreshToken },
          accessToken
            ? { headers: { Authorization: `Bearer ${accessToken}` } }
            : undefined
        )
        .catch(() => undefined);
    }
  },

  setUser: (user) => set({ user }),
  clearError: () => set({ error: null }),
}));
