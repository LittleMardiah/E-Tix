'use client';

import { useEffect, useState } from 'react';
import { useRouter } from 'next/navigation';
import { apiClient } from '@/lib/api/client';
import { useAuthStore, type AuthUser } from '@/lib/stores/authStore';

const ROLE_HOME: Record<string, string> = {
  admin: '/admin/dashboard',
  event_organizer: '/eo/dashboard',
  customer: '/customer/dashboard',
  gate_staff: '/gate/scanner',
};

interface MeResponse {
  user_id: string;
  email: string;
  name: string;
  role: string;
  tier?: string;
}

/**
 * Client-side role gate for a route group.
 *
 * The access token survives in localStorage but the Zustand store starts empty
 * on every reload, so the role is re-hydrated from GET /users/me. Returns true
 * until the role is settled; the caller must render a loading state meanwhile,
 * otherwise protected content flashes before the redirect.
 */
export function useRoleGuard(...roles: string[]): boolean {
  const router = useRouter();
  const user = useAuthStore((state) => state.user);
  const setUser = useAuthStore((state) => state.setUser);
  const [checking, setChecking] = useState(true);
  const allowedKey = roles.join(',');

  useEffect(() => {
    let active = true;
    const allowed = allowedKey.split(',');

    const settle = (current: AuthUser) => {
      if (!active) return;
      if (!allowed.includes(current.role)) {
        router.replace(ROLE_HOME[current.role] ?? '/customer/dashboard');
        return;
      }
      setChecking(false);
    };

    if (user) {
      settle(user);
      return () => {
        active = false;
      };
    }

    apiClient
      .get<{ data: MeResponse }>('/users/me')
      .then((response) => {
        const me = response.data.data;
        const next: AuthUser = {
          user_id: me.user_id,
          email: me.email,
          name: me.name,
          role: me.role,
          tier: me.tier,
        };
        setUser(next);
        settle(next);
      })
      .catch(() => {
        if (active) router.replace('/auth/login');
      });

    return () => {
      active = false;
    };
  }, [allowedKey, router, setUser, user]);

  return checking;
}
