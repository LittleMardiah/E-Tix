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
  const [pendingLock, setPendingLock] = useState<StuckLock | null>(null);
  const [toast, setToast] = useState('');

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

  useEffect(() => {
    if (!toast) return;
    const timer = setTimeout(() => setToast(''), 4000);
    return () => clearTimeout(timer);
  }, [toast]);

  const requestRelease = (lock: StuckLock) => {
    setError('');
    setMessage('');
    setPendingLock(lock);
  };

  const confirmRelease = async () => {
    const lock = pendingLock;
    if (!lock) return;
    setPendingLock(null);

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
      const successText = `Kursi untuk booking ${lock.booking_id} berhasil dirilis.`;
      setMessage(successText);
      setToast(successText);
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
                onClick={() => requestRelease(lock)}
                isLoading={releasingId === lock.booking_id}
              >
                Release
              </Button>
            </Card>
          ))}
        </div>
      )}

      {pendingLock && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4">
          <div
            role="dialog"
            aria-modal="true"
            aria-labelledby="force-release-title"
            className="w-full max-w-md rounded-2xl border border-[#E2E8F0] bg-white p-6 shadow-2xl"
          >
            <h2 id="force-release-title" className="text-lg font-bold text-[#171717]">
              Konfirmasi Force Release
            </h2>
            <p className="mt-2 text-sm text-[#4D4D4D]">
              Kursi untuk booking <span className="font-mono">{pendingLock.booking_id}</span> akan
              dirilis dan booking terkunci dibatalkan.
            </p>
            <p className="mt-2 text-sm text-[#4D4D4D]">
              {pendingLock.section_name} &bull; Kursi: {pendingLock.seat_positions.join(", ")}
            </p>
            <p className="mt-2 text-sm text-[#4D4D4D]">
              Alasan: <span className="font-medium text-[#171717]">{reason || "-"}</span>
            </p>
            <div className="mt-6 flex justify-end gap-2">
              <Button variant="outline" onClick={() => setPendingLock(null)}>
                Batal
              </Button>
              <Button onClick={() => void confirmRelease()}>Ya, Release</Button>
            </div>
          </div>
        </div>
      )}

      {toast && (
        <div
          role="status"
          className="fixed bottom-4 right-4 z-[60] max-w-sm rounded-xl border border-green-500/40 bg-green-500/15 px-4 py-3 text-sm text-green-800 shadow-xl"
        >
          {toast}
        </div>
      )}
    </div>
  );
}
