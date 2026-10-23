'use client';

import { useCallback, useEffect, useState } from 'react';
import {
  apiClient,
  createIdempotencyKey,
  getApiErrorMessage,
} from '@/lib/api/client';
import { Alert } from '@/components/ui/Alert';
import { Button } from '@/components/ui/Button';
import { Card } from '@/components/ui/Card';
import { Input } from '@/components/ui/Input';

const ADMIN_2FA_STORAGE_KEY = 'e-tix-flash:admin-2fa-token';

function readAdmin2FAToken(): string {
  if (typeof window === 'undefined') return '';
  return window.sessionStorage.getItem(ADMIN_2FA_STORAGE_KEY) ?? '';
}

function saveAdmin2FAToken(token: string): void {
  if (typeof window === 'undefined') return;
  if (token) {
    window.sessionStorage.setItem(ADMIN_2FA_STORAGE_KEY, token);
  } else {
    window.sessionStorage.removeItem(ADMIN_2FA_STORAGE_KEY);
  }
}

interface AdminUser {
  user_id: string;
  email: string;
  phone: string;
  name: string;
  role: string;
  status: string;
  loyalty_tier: string;
  total_purchases: number;
  created_at: string;
  last_login_at?: string;
  suspended_reason?: string;
}

interface UserListData {
  users: AdminUser[];
}

export default function AdminUsers() {
  const [users, setUsers] = useState<AdminUser[]>([]);
  const [twoFA, setTwoFA] = useState('');
  const [reason, setReason] = useState('Suspicious activity');
  const [loading, setLoading] = useState(true);
  const [actingOn, setActingOn] = useState('');
  const [error, setError] = useState('');
  const [message, setMessage] = useState('');

  const fetchUsers = useCallback(async (token: string) => {
    setLoading(true);
    setError('');
    try {
      const response = await apiClient.get<{ data: UserListData }>(
        '/admin/users',
        {
          headers: token ? { 'X-Admin-2FA-Token': token } : undefined,
        }
      );
      setUsers(response.data.data.users ?? []);
    } catch (err: unknown) {
      setError(getApiErrorMessage(err, 'Gagal memuat daftar user.'));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    const stored = readAdmin2FAToken();
    setTwoFA(stored);
    void fetchUsers(stored);
  }, [fetchUsers]);

  const handleTokenChange = (token: string) => {
    setTwoFA(token);
    saveAdmin2FAToken(token);
  };

  const updateUserStatus = async (user: AdminUser, suspend: boolean) => {
    setActingOn(user.user_id);
    setError('');
    setMessage('');
    try {
      const headers = {
        'X-Idempotency-Key': createIdempotencyKey(),
        ...(twoFA ? { 'X-Admin-2FA-Token': twoFA } : {}),
      };
      const response = suspend
        ? await apiClient.patch<{ data: { status: string; suspended_reason?: string } }>(
            `/admin/users/${user.user_id}/suspend`,
            { reason },
            { headers }
          )
        : await apiClient.patch<{
            data: { status: string; suspended_reason?: string };
          }>(`/admin/users/${user.user_id}/unsuspend`, undefined, { headers });

      setUsers((current) =>
        current.map((item) =>
          item.user_id === user.user_id
            ? {
                ...item,
                status: response.data.data.status,
                suspended_reason: suspend
                  ? response.data.data.suspended_reason ?? reason
                  : undefined,
              }
            : item
        )
      );
      setMessage(
        suspend
          ? `${user.name} berhasil disuspend.`
          : `${user.name} berhasil diaktifkan kembali.`
      );
    } catch (err: unknown) {
      setError(getApiErrorMessage(err, 'Aksi user gagal. Periksa 2FA.'));
    } finally {
      setActingOn('');
    }
  };

  return (
    <div className="p-6 md:p-10 max-w-6xl mx-auto">
      <h1 className="text-3xl font-bold mb-6">User Management</h1>

      <div className="grid grid-cols-1 sm:grid-cols-2 gap-4 mb-6 sm:items-end">
        <Input
          label="2FA Token"
          value={twoFA}
          placeholder="Kode TOTP admin"
          onChange={(event) => handleTokenChange(event.target.value)}
        />
        <Input
          label="Suspend Reason"
          value={reason}
          onChange={(event) => setReason(event.target.value)}
        />
      </div>

      {error && (
        <Alert variant="error" className="mb-4">
          {error}
        </Alert>
      )}
      {message && (
        <Alert variant="success" className="mb-4">
          {message}
        </Alert>
      )}

      {loading ? (
        <p className="text-center text-gray-500 py-10">Memuat user...</p>
      ) : users.length === 0 ? (
        <Card>
          <p className="text-center text-gray-500">Belum ada user.</p>
        </Card>
      ) : (
        <div className="grid gap-4">
          {users.map((user) => (
            <Card
              key={user.user_id}
              className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between"
            >
              <div>
                <p className="font-bold">
                  {user.name} ({user.email})
                </p>
                <p className="text-sm text-gray-500">
                  {user.phone || '-'} &bull; Role: {user.role} &bull; Status:{' '}
                  <span
                    className={
                      user.status === 'SUSPENDED'
                        ? 'font-semibold text-red-600'
                        : 'font-semibold text-green-600'
                    }
                  >
                    {user.status}
                  </span>
                </p>
                {user.suspended_reason && (
                  <p className="text-sm text-red-600 mt-1">
                    Alasan: {user.suspended_reason}
                  </p>
                )}
              </div>
              <Button
                variant={user.status === 'SUSPENDED' ? 'secondary' : 'outline'}
                onClick={() =>
                  void updateUserStatus(user, user.status !== 'SUSPENDED')
                }
                isLoading={actingOn === user.user_id}
                disabled={!['ACTIVE', 'SUSPENDED'].includes(user.status)}
              >
                {user.status === 'SUSPENDED' ? 'Unsuspend' : 'Suspend'}
              </Button>
            </Card>
          ))}
        </div>
      )}
    </div>
  );
}
