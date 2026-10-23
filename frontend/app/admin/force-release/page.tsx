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

interface StuckLock {
  booking_id: string;
  event_id: string;
  section_id: string;
  section_name: string;
  layout_version: number;
  seat_positions: number[];
  locked_since: string;
  duration_minutes: number;
  status: string;
}

interface StuckLocksData {
  stuck_locks: StuckLock[];
}

function formatDateTime(value: string): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime())
    ? value
    : date.toLocaleString('id-ID', {
        dateStyle: 'medium',
        timeStyle: 'short',
      });
}

export default function AdminForceRelease() {
  const [locks, setLocks] = useState<StuckLock[]>([]);
  const [twoFA, setTwoFA] = useState('');
  const [reason, setReason] = useState('Stuck lock timeout');
  const [loading, setLoading] = useState(true);
  const [releasingId, setReleasingId] = useState('');
  const [error, setError] = useState('');
  const [message, setMessage] = useState('');

  const fetchLocks = useCallback(async (token: string) => {
    setLoading(true);
    setError('');
    try {
      const response = await apiClient.get<{ data: StuckLocksData }>(
        '/admin/stuck-locks',
        {
          headers: token ? { 'X-Admin-2FA-Token': token } : undefined,
        }
      );
      setLocks(response.data.data.stuck_locks ?? []);
    } catch (err: unknown) {
      setError(getApiErrorMessage(err, 'Gagal memuat stuck locks.'));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    const stored = readAdmin2FAToken();
    setTwoFA(stored);
    void fetchLocks(stored);
  }, [fetchLocks]);

  const handleTokenChange = (token: string) => {
    setTwoFA(token);
    saveAdmin2FAToken(token);
  };

  const releaseLock = async (lock: StuckLock) => {
    if (
      !window.confirm(
        `Release kursi untuk booking ${lock.booking_id}? Tindakan ini membatalkan booking terkunci.`
      )
    ) {
      return;
    }

    setReleasingId(lock.booking_id);
    setError('');
    setMessage('');
    try {
      await apiClient.post(
        '/admin/force-release',
        { booking_id: lock.booking_id, reason },
        {
          headers: {
            'X-Idempotency-Key': createIdempotencyKey(),
            ...(twoFA ? { 'X-Admin-2FA-Token': twoFA } : {}),
          },
        }
      );
      setLocks((current) =>
        current.filter((item) => item.booking_id !== lock.booking_id)
      );
      setMessage(`Kursi untuk booking ${lock.booking_id} berhasil dirilis.`);
    } catch (err: unknown) {
      setError(getApiErrorMessage(err, 'Gagal force release. Periksa 2FA.'));
    } finally {
      setReleasingId('');
    }
  };

  return (
    <div className="p-6 md:p-10 max-w-4xl mx-auto">
      <h1 className="text-3xl font-bold mb-6">Force Seat Release</h1>

      <div className="grid grid-cols-1 sm:grid-cols-2 gap-4 mb-6 sm:items-end">
        <Input
          label="2FA Token"
          value={twoFA}
          placeholder="Kode TOTP admin"
          onChange={(event) => handleTokenChange(event.target.value)}
        />
        <Input
          label="Reason"
          value={reason}
          onChange={(event) => setReason(event.target.value)}
        />
      </div>
      <div className="mb-6">
        <Button
          variant="outline"
          onClick={() => void fetchLocks(twoFA)}
          isLoading={loading}
        >
          Muat Ulang
        </Button>
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
        <p className="text-center text-gray-500 py-10">Memuat stuck locks...</p>
      ) : locks.length === 0 ? (
        <Card>
          <p className="text-center text-gray-500">Tidak ada stuck lock.</p>
        </Card>
      ) : (
        <div className="grid gap-4">
          {locks.map((lock) => (
            <Card
              key={lock.booking_id}
              className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between"
            >
              <div>
                <p className="font-bold">Booking: {lock.booking_id}</p>
                <p className="text-sm text-gray-500">
                  {lock.section_name} &bull; Kursi:{' '}
                  {lock.seat_positions.join(', ')} &bull; {lock.duration_minutes} menit
                </p>
                <p className="text-sm text-gray-500">
                  Terkunci sejak {formatDateTime(lock.locked_since)} &bull; Status:{' '}
                  {lock.status}
                </p>
              </div>
              <Button
                variant="outline"
                onClick={() => void releaseLock(lock)}
                isLoading={releasingId === lock.booking_id}
              >
                Release
              </Button>
            </Card>
          ))}
        </div>
      )}
    </div>
  );
}
