'use client';

import { useState } from 'react';
import { useParams, useRouter } from 'next/navigation';
import {
  apiClient,
  createIdempotencyKey,
  getApiErrorMessage,
} from '@/lib/api/client';
import { Alert } from '@/components/ui/Alert';
import { Button } from '@/components/ui/Button';
import { Card } from '@/components/ui/Card';

/**
 * Cancel Resale Listing (HALAMAN 7.3).
 * Backed by DELETE /resale/listings/{resale_id} (API CONTRACT 12.4).
 */
export default function CancelResalePage() {
  const params = useParams<{ resaleId: string }>();
  const resaleId = params?.resaleId ?? '';
  const router = useRouter();

  const [loading, setLoading] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [error, setError] = useState('');

  const handleCancel = async () => {
    if (!resaleId) {
      setError('Resale ID tidak valid.');
      return;
    }

    setLoading(true);
    setError('');
    try {
      await apiClient.delete(`/resale/listings/${resaleId}`, {
        headers: { 'X-Idempotency-Key': createIdempotencyKey() },
      });
      router.push('/customer/my-tickets');
    } catch (err) {
      setError(getApiErrorMessage(err, 'Gagal membatalkan listing'));
      setConfirming(false);
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="min-h-screen bg-gray-50 text-gray-900 flex items-center justify-center p-4">
      <Card className="w-full max-w-md">
        <div className="mb-4">
          <Button href="/customer/my-tickets" variant="outline">
            &larr; Tiket Saya
          </Button>
        </div>

        <h1 className="text-2xl font-bold mb-4">Batalkan Resale</h1>
        <p className="text-gray-500 mb-6">
          Tiket akan dikembalikan ke akun Anda dan dihapus dari marketplace.
        </p>

        {error && (
          <Alert variant="error" className="mb-4">
            {error}
          </Alert>
        )}

        {confirming ? (
          <div className="space-y-4">
            <Alert variant="warning" title="Konfirmasi Pembatalan">
              Yakin ingin menghapus tiket ini dari marketplace?
            </Alert>
            <Button
              className="w-full bg-red-600 hover:bg-red-700 focus:ring-red-500"
              onClick={() => void handleCancel()}
              isLoading={loading}
            >
              Ya, Batalkan
            </Button>
            <Button
              variant="outline"
              className="w-full"
              onClick={() => setConfirming(false)}
              disabled={loading}
            >
              Tidak
            </Button>
          </div>
        ) : (
          <Button
            variant="outline"
            className="w-full text-red-600 border-red-600 hover:bg-red-50"
            onClick={() => setConfirming(true)}
          >
            Batalkan Listing
          </Button>
        )}
      </Card>
    </div>
  );
}
