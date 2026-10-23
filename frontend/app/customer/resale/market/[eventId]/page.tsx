'use client';

import { useCallback, useEffect, useState } from 'react';
import { useParams } from 'next/navigation';
import {
  apiClient,
  createIdempotencyKey,
  getApiErrorMessage,
} from '@/lib/api/client';
import { Alert } from '@/components/ui/Alert';
import { Button } from '@/components/ui/Button';
import { Card } from '@/components/ui/Card';
import { formatRupiah } from '@/lib/format';

interface ResaleListing {
  resale_id: string;
  ticket_id: string;
  seller_name: string;
  seat: string;
  resale_price: string;
  platform_fee: string;
  seller_payout: string;
  original_price: string;
  created_at: string;
}

interface ResaleListingsPayload {
  listings: ResaleListing[];
}

interface BuyResalePayload {
  resale_id: string;
  status: string;
  reservation_expires_at: string;
  payment_link?: string;
}

/**
 * Browse Resale Market (HALAMAN 7.2).
 * Backed by GET /resale?event_id=&status=PENDING and POST /resale/{id}/buy
 * (API CONTRACT 12.2 and 12.3).
 */
export default function ResaleMarketPage() {
  const params = useParams<{ eventId: string }>();
  const eventId = params?.eventId ?? '';

  const [listings, setListings] = useState<ResaleListing[]>([]);
  const [loading, setLoading] = useState(true);
  const [buyingId, setBuyingId] = useState('');
  const [error, setError] = useState('');
  const [message, setMessage] = useState('');
  const [paymentLink, setPaymentLink] = useState('');

  const loadListings = useCallback(async () => {
    if (!eventId) {
      setLoading(false);
      return;
    }

    setLoading(true);
    setError('');
    try {
      const response = await apiClient.get<{ data: ResaleListingsPayload }>(
        '/resale',
        { params: { event_id: eventId, status: 'PENDING' } }
      );
      setListings(response.data.data.listings ?? []);
    } catch (err) {
      setError(getApiErrorMessage(err, 'Gagal memuat resale market'));
    } finally {
      setLoading(false);
    }
  }, [eventId]);

  useEffect(() => {
    void loadListings();
  }, [loadListings]);

  const buy = async (resaleId: string) => {
    setBuyingId(resaleId);
    setError('');
    setMessage('');
    setPaymentLink('');
    try {
      const response = await apiClient.post<{ data: BuyResalePayload }>(
        `/resale/${resaleId}/buy`,
        {},
        { headers: { 'X-Idempotency-Key': createIdempotencyKey() } }
      );
      setMessage(
        'Berhasil di-reserve. Selesaikan pembayaran sebelum reservasi berakhir.'
      );
      setPaymentLink(response.data.data.payment_link ?? '');
      await loadListings();
    } catch (err) {
      setError(getApiErrorMessage(err, 'Gagal beli tiket resale'));
    } finally {
      setBuyingId('');
    }
  };

  return (
    <div className="min-h-screen bg-gray-50 text-gray-900">
      <header className="bg-white border-b border-gray-200">
        <div className="max-w-4xl mx-auto px-4 py-4 flex items-center justify-between">
          <Button href={`/customer/events/${eventId}`} variant="outline">
            &larr; Detail Event
          </Button>
          <Button variant="outline" onClick={() => void loadListings()}>
            Muat Ulang
          </Button>
        </div>
      </header>

      <main className="max-w-4xl mx-auto px-4 py-8">
        <h1 className="text-3xl font-bold mb-2">Resale Market</h1>
        <p className="text-sm text-gray-500 mb-6">
          Tiket resale resmi dari sesama pembeli. Harga sudah dibatasi oleh
          platform.
        </p>

        {message && (
          <Alert variant="success" className="mb-4">
            {message}{' '}
            {paymentLink && (
              <a
                href={paymentLink}
                target="_blank"
                rel="noreferrer"
                className="font-semibold underline"
              >
                Buka pembayaran
              </a>
            )}
          </Alert>
        )}

        {error && (
          <Alert variant="error" className="mb-6">
            {error}
          </Alert>
        )}

        {loading ? (
          <p className="text-gray-500 py-10 text-center">
            Memuat marketplace...
          </p>
        ) : !eventId ? (
          <Alert variant="error">Event ID tidak valid.</Alert>
        ) : listings.length === 0 ? (
          <Card className="text-center">
            <p className="text-gray-600">Tidak ada tiket resale saat ini.</p>
          </Card>
        ) : (
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            {listings.map((listing) => (
              <Card key={listing.resale_id}>
                <div className="flex items-start justify-between gap-3">
                  <div>
                    <h3 className="font-bold text-gray-900">
                      {listing.seat}
                    </h3>
                    <p className="text-sm text-gray-500">
                      Seller: {listing.seller_name}
                    </p>
                  </div>
                  <span className="text-xs font-semibold px-2 py-1 rounded bg-blue-100 text-blue-700">
                    RESALE
                  </span>
                </div>

                <dl className="mt-3 space-y-1 text-sm text-gray-600">
                  <div className="flex justify-between">
                    <dt>Harga resale</dt>
                    <dd className="text-lg font-bold text-blue-600">
                      {formatRupiah(listing.resale_price)}
                    </dd>
                  </div>
                  <div className="flex justify-between">
                    <dt>Harga asli</dt>
                    <dd className="font-medium text-gray-900">
                      {formatRupiah(listing.original_price)}
                    </dd>
                  </div>
                </dl>

                <Button
                  className="w-full mt-4"
                  onClick={() => void buy(listing.resale_id)}
                  isLoading={buyingId === listing.resale_id}
                >
                  Beli Tiket
                </Button>
              </Card>
            ))}
          </div>
        )}
      </main>
    </div>
  );
}
