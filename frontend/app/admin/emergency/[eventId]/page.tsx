'use client';

import { useEffect, useState } from 'react';
import { useParams } from 'next/navigation';
import {
  apiClient,
  createIdempotencyKey,
  getApiErrorMessage,
} from '@/lib/api/client';
import { Alert } from '@/components/ui/Alert';
import { Button } from '@/components/ui/Button';
import { Card } from '@/components/ui/Card';
import { Input } from '@/components/ui/Input';
import { formatRupiah } from '@/lib/format';

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

interface BulkCancelData {
  event_id: string;
  cancelled_tickets: number;
  refund_total: string;
  status: string;
}

export default function AdminEmergencyBulkCancel() {
  const params = useParams<{ eventId: string }>();
  const eventId = params?.eventId ?? '';

  const [twoFA, setTwoFA] = useState('');
  const [reason, setReason] = useState('Price error');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [result, setResult] = useState<BulkCancelData | null>(null);

  useEffect(() => {
    setTwoFA(readAdmin2FAToken());
  }, []);

  const handleTokenChange = (token: string) => {
    setTwoFA(token);
    saveAdmin2FAToken(token);
  };

  const cancel = async () => {
    if (!eventId) return;
    if (
      !window.confirm(
        'Yakin mau membatalkan SEMUA tiket event ini? Tindakan ini tidak bisa dibatalkan.'
      )
    ) {
      return;
    }

    setLoading(true);
    setError('');
    setResult(null);
    try {
      const response = await apiClient.post<{ data: BulkCancelData }>(
        `/admin/events/${eventId}/bulk-cancel`,
        { reason },
        {
          headers: {
            'X-Idempotency-Key': createIdempotencyKey(),
            ...(twoFA ? { 'X-Admin-2FA-Token': twoFA } : {}),
          },
        }
      );
      setResult(response.data.data);
    } catch (err: unknown) {
      setError(
        getApiErrorMessage(err, 'Gagal melakukan bulk cancel. Periksa 2FA.')
      );
    } finally {
      setLoading(false);
    }
  };

  if (!eventId) {
    return (
      <div className="p-10 max-w-lg mx-auto">
        <Alert variant="error">Event ID tidak valid.</Alert>
      </div>
    );
  }

  return (
    <div className="p-6 md:p-10 max-w-lg mx-auto">
      <h1 className="text-3xl font-bold text-red-600 mb-2">
        Emergency Bulk Cancel
      </h1>
      <p className="mb-6 text-gray-600">
        Aksi ini akan membatalkan semua tiket yang belum USED dan memicu refund.
        Event ID: <span className="font-mono text-sm">{eventId}</span>
      </p>

      {error && (
        <Alert variant="error" className="mb-4">
          {error}
        </Alert>
      )}

      {result && (
        <Alert variant="success" title="Bulk cancel dijadwalkan" className="mb-4">
          <p>Status: {result.status}</p>
          <p> Tiket dibatalkan: {result.cancelled_tickets}</p>
          <p> Total refund: {formatRupiah(result.refund_total)}</p>
        </Alert>
      )}

      <Card>
        <div className="space-y-4">
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
          <Button
            className="w-full bg-red-600 hover:bg-red-700 focus:ring-red-500"
            onClick={() => void cancel()}
            isLoading={loading}
            disabled={!reason.trim()}
          >
            CONFIRM CANCELLATION
          </Button>
        </div>
      </Card>
    </div>
  );
}
