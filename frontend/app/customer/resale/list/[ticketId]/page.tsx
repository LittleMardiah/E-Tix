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
import { Input } from '@/components/ui/Input';
import { formatRupiah } from '@/lib/format';

/**
 * List Ticket for Resale (HALAMAN 7.1).
 * Backed by POST /resale (API CONTRACT 12.1).
 * The backend enforces the price cap (max +10% of the original price).
 */
export default function ResaleListPage() {
  const params = useParams<{ ticketId: string }>();
  const ticketId = params?.ticketId ?? '';
  const router = useRouter();

  const [price, setPrice] = useState('1050000');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  const handleList = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!ticketId) {
      setError('Ticket ID tidak valid.');
      return;
    }

    setLoading(true);
    setError('');
    try {
      await apiClient.post(
        '/resale',
        { ticket_id: ticketId, resale_price: price },
        { headers: { 'X-Idempotency-Key': createIdempotencyKey() } }
      );
      router.push('/customer/my-tickets');
    } catch (err) {
      setError(getApiErrorMessage(err, 'Gagal list tiket'));
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

        <h1 className="text-2xl font-bold mb-2">Jual Tiket</h1>
        <p className="text-sm text-gray-500 mb-6">
          Masukkan harga jual. Harga maksimum adalah +10% dari harga asli
          tiket.
        </p>

        {error && (
          <Alert variant="error" className="mb-4">
            {error}
          </Alert>
        )}

        <form onSubmit={handleList} className="space-y-4">
          <Input
            label="Harga Resale (Rp)"
            type="number"
            min={1}
            step={1000}
            value={price}
            onChange={(event) => setPrice(event.target.value)}
            required
          />
          <p className="text-sm text-gray-500">
            Harga yang tampil di marketplace: {formatRupiah(price)}
          </p>
          <Button type="submit" className="w-full" isLoading={loading}>
            Daftarkan
          </Button>
        </form>
      </Card>
    </div>
  );
}
