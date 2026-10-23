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

interface BlacklistEntry {
  id: string;
  type: string;
  value: string;
  reason?: string;
  added_by?: string;
  added_at: string;
  expires_at?: string;
}

interface BlacklistData {
  entries: BlacklistEntry[];
}

function formatDateTime(value?: string): string {
  if (!value) return '-';
  const date = new Date(value);
  return Number.isNaN(date.getTime())
    ? value
    : date.toLocaleString('id-ID', {
        dateStyle: 'medium',
        timeStyle: 'short',
      });
}

export default function AdminBlacklist() {
  const [entries, setEntries] = useState<BlacklistEntry[]>([]);
  const [twoFA, setTwoFA] = useState('');
  const [type, setType] = useState('ip');
  const [value, setValue] = useState('');
  const [reason, setReason] = useState('');
  const [expiresAt, setExpiresAt] = useState('');
  const [loading, setLoading] = useState(true);
  const [adding, setAdding] = useState(false);
  const [removingId, setRemovingId] = useState('');
  const [error, setError] = useState('');
  const [message, setMessage] = useState('');

  const fetchBlacklist = useCallback(async (token: string) => {
    setLoading(true);
    setError('');
    try {
      const response = await apiClient.get<{ data: BlacklistData }>(
        '/admin/blacklist',
        {
          headers: token ? { 'X-Admin-2FA-Token': token } : undefined,
        }
      );
      setEntries(response.data.data.entries ?? []);
    } catch (err: unknown) {
      setError(getApiErrorMessage(err, 'Gagal memuat blacklist.'));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    const stored = readAdmin2FAToken();
    setTwoFA(stored);
    void fetchBlacklist(stored);
  }, [fetchBlacklist]);

  const handleTokenChange = (token: string) => {
    setTwoFA(token);
    saveAdmin2FAToken(token);
  };

  const addEntry = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setAdding(true);
    setError('');
    setMessage('');
    try {
      await apiClient.post(
        '/admin/blacklist',
        {
          type,
          value: value.trim(),
          reason: reason.trim(),
          ...(expiresAt
            ? { expires_at: new Date(expiresAt).toISOString() }
            : {}),
        },
        {
          headers: {
            'X-Idempotency-Key': createIdempotencyKey(),
            ...(twoFA ? { 'X-Admin-2FA-Token': twoFA } : {}),
          },
        }
      );
      setValue('');
      setReason('');
      setExpiresAt('');
      setMessage('Entri blacklist berhasil ditambahkan.');
      await fetchBlacklist(twoFA);
    } catch (err: unknown) {
      setError(getApiErrorMessage(err, 'Gagal menambahkan blacklist.'));
    } finally {
      setAdding(false);
    }
  };

  const removeEntry = async (entry: BlacklistEntry) => {
    if (!window.confirm(`Hapus blacklist ${entry.type} ${entry.value}?`)) return;
    setRemovingId(entry.id);
    setError('');
    setMessage('');
    try {
      await apiClient.delete(`/admin/blacklist/${entry.id}`, {
        headers: {
          'X-Idempotency-Key': createIdempotencyKey(),
          ...(twoFA ? { 'X-Admin-2FA-Token': twoFA } : {}),
        },
      });
      setEntries((current) =>
        current.filter((item) => item.id !== entry.id)
      );
      setMessage('Entri blacklist berhasil dihapus.');
    } catch (err: unknown) {
      setError(getApiErrorMessage(err, 'Gagal menghapus blacklist.'));
    } finally {
      setRemovingId('');
    }
  };

  return (
    <div className="p-6 md:p-10 max-w-6xl mx-auto">
      <h1 className="text-3xl font-bold mb-6">Blacklist Management</h1>

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

      <Card className="mb-6">
        <h2 className="text-xl font-bold mb-4">Tambah Entri</h2>
        <form onSubmit={addEntry} className="space-y-4">
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
            <div className="flex flex-col gap-1">
              <label htmlFor="blacklist-type" className="text-sm font-medium text-gray-700">
                Type
              </label>
              <select
                id="blacklist-type"
                value={type}
                onChange={(event) => setType(event.target.value)}
                className="px-3 py-2 border border-gray-300 rounded-md bg-white text-gray-900 focus:outline-none focus:ring-2 focus:ring-blue-500"
              >
                <option value="ip">IP</option>
                <option value="device_fingerprint">Device Fingerprint</option>
              </select>
            </div>
            <Input
              label="Value"
              value={value}
              required
              placeholder={type === 'ip' ? '203.0.113.42' : 'device-fingerprint'}
              onChange={(event) => setValue(event.target.value)}
            />
          </div>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
            <Input
              label="Reason"
              value={reason}
              required
              onChange={(event) => setReason(event.target.value)}
            />
            <Input
              label="Expires At (opsional)"
              type="datetime-local"
              value={expiresAt}
              onChange={(event) => setExpiresAt(event.target.value)}
            />
          </div>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4 sm:items-end">
            <Input
              label="2FA Token (opsional)"
              value={twoFA}
              placeholder="Kode TOTP admin"
              onChange={(event) => handleTokenChange(event.target.value)}
            />
            <Button type="submit" isLoading={adding}>
              Tambah Blacklist
            </Button>
          </div>
        </form>
      </Card>

      <Card>
        <h2 className="text-xl font-bold mb-4">Entri Aktif</h2>
        {loading ? (
          <p className="text-center text-gray-500 py-6">Memuat blacklist...</p>
        ) : entries.length === 0 ? (
          <p className="text-center text-gray-500 py-6">Blacklist kosong.</p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b text-left text-gray-500">
                  <th className="py-2 pr-4">Type</th>
                  <th className="py-2 pr-4">Value</th>
                  <th className="py-2 pr-4">Reason</th>
                  <th className="py-2 pr-4">Added</th>
                  <th className="py-2 pr-4">Expires</th>
                  <th className="py-2">Action</th>
                </tr>
              </thead>
              <tbody>
                {entries.map((entry) => (
                  <tr key={entry.id} className="border-b">
                    <td className="py-2 pr-4 font-medium">{entry.type}</td>
                    <td className="py-2 pr-4">{entry.value}</td>
                    <td className="py-2 pr-4 text-gray-500">
                      {entry.reason || '-'}
                    </td>
                    <td className="py-2 pr-4">{formatDateTime(entry.added_at)}</td>
                    <td className="py-2 pr-4">
                      {entry.expires_at ? formatDateTime(entry.expires_at) : 'Permanent'}
                    </td>
                    <td className="py-2">
                      <Button
                        variant="outline"
                        onClick={() => void removeEntry(entry)}
                        isLoading={removingId === entry.id}
                      >
                        Hapus
                      </Button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
    </div>
  );
}
