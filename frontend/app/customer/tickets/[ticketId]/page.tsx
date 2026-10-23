'use client';

import { useCallback, useEffect, useState } from 'react';
import Image from 'next/image';
import { useParams } from 'next/navigation';
import { apiClient, getApiErrorMessage } from '@/lib/api/client';
import { Alert } from '@/components/ui/Alert';
import { Button } from '@/components/ui/Button';
import { Card } from '@/components/ui/Card';

interface TicketDetail {
  id: string;
  event_id: string;
  event_name: string;
  section_id: string;
  section_name: string;
  seat_position: number;
  status: string;
  owner_user_id: string;
  fallback_pin: string;
  qr_updated_at?: string;
  created_at: string;
}

interface QRResponse {
  qr_data: string;
  expires_at: string;
  refresh_in: number;
}

function isImageData(value: string): boolean {
  return value.startsWith('data:image/');
}

export default function TicketDetailPage() {
  const params = useParams<{ ticketId: string }>();
  const ticketId = params?.ticketId ?? '';

  const [ticket, setTicket] = useState<TicketDetail | null>(null);
  const [qr, setQr] = useState<QRResponse | null>(null);
  const [secondsLeft, setSecondsLeft] = useState<number | null>(null);
  const [loading, setLoading] = useState(true);
  const [qrLoading, setQrLoading] = useState(false);
  const [error, setError] = useState('');
  const [qrError, setQrError] = useState('');

  useEffect(() => {
    if (!ticketId) {
      setError('Ticket ID tidak valid.');
      setLoading(false);
      return;
    }

    let active = true;
    setLoading(true);
    setError('');

    apiClient
      .get<{ data: TicketDetail }>(`/tickets/${ticketId}`)
      .then((response) => {
        if (active) setTicket(response.data.data);
      })
      .catch((err) => {
        if (active) setError(getApiErrorMessage(err, 'Gagal memuat tiket'));
      })
      .finally(() => {
        if (active) setLoading(false);
      });

    return () => {
      active = false;
    };
  }, [ticketId]);

  const fetchQr = useCallback(async () => {
    if (!ticketId) return;
    setQrLoading(true);
    setQrError('');
    try {
      const response = await apiClient.get<{ data: QRResponse }>(
        `/tickets/${ticketId}/qr`
      );
      setQr(response.data.data);
    } catch (err) {
      setQrError(getApiErrorMessage(err, 'Gagal memuat QR dinamis'));
    } finally {
      setQrLoading(false);
    }
  }, [ticketId]);

  useEffect(() => {
    if (!ticket) return;
    if (ticket.status !== 'CONFIRMED' && ticket.status !== 'RESALE_PENDING') {
      return;
    }
    void fetchQr();
  }, [ticket, fetchQr]);

  useEffect(() => {
    if (!qr) return;

    const deadline = new Date(qr.expires_at).getTime();
    const fallbackMs = Math.max(1, qr.refresh_in || 28) * 1000;
    const ttlMs = Number.isFinite(deadline)
      ? deadline - Date.now()
      : fallbackMs;

    const tick = () => {
      const remaining = Number.isFinite(deadline)
        ? Math.max(0, Math.ceil((deadline - Date.now()) / 1000))
        : Math.max(0, Math.ceil(fallbackMs / 1000));
      setSecondsLeft(remaining);
    };

    tick();
    const countdown = setInterval(tick, 1000);
    const refresh = setTimeout(() => void fetchQr(), Math.max(1000, ttlMs + 250));

    return () => {
      clearInterval(countdown);
      clearTimeout(refresh);
    };
  }, [qr, fetchQr]);

  if (loading) {
    return (
      <div className="min-h-screen bg-gray-50 flex items-center justify-center p-4">
        <p className="text-gray-500">Memuat tiket...</p>
      </div>
    );
  }

  if (error || !ticket) {
    return (
      <div className="min-h-screen bg-gray-50 text-gray-900">
        <header className="bg-white border-b border-gray-200">
          <div className="max-w-lg mx-auto px-4 py-4">
            <Button href="/customer/my-tickets" variant="outline">
              &larr; Tiket Saya
            </Button>
          </div>
        </header>
        <main className="max-w-lg mx-auto px-4 py-10">
          <Alert variant="error">{error || 'Tiket tidak ditemukan'}</Alert>
        </main>
      </div>
    );
  }

  return (
    <div className="min-h-screen bg-gray-50 text-gray-900">
      <header className="bg-white border-b border-gray-200">
        <div className="max-w-lg mx-auto px-4 py-4 flex items-center justify-between">
          <Button href="/customer/my-tickets" variant="outline">
            &larr; Tiket Saya
          </Button>
          <span className="text-xs font-semibold px-2 py-1 rounded bg-gray-100 text-gray-700">
            {ticket.status}
          </span>
        </div>
      </header>

      <main className="max-w-lg mx-auto px-4 py-8">
        <Card className="text-center">
          <h1 className="text-2xl font-bold text-gray-900">
            {ticket.event_name}
          </h1>
          <p className="text-gray-500 mt-1">
            {ticket.section_name} &mdash; Seat {ticket.seat_position}
          </p>

          <div className="mt-6">
            <h2 className="text-sm font-semibold text-gray-700 mb-3">
              QR Code
            </h2>
            <div className="bg-white p-4 rounded-lg border-2 border-dashed border-gray-300">
              {qrLoading && !qr ? (
                <p className="py-16 text-gray-500">Memuat QR...</p>
              ) : qr ? (
                isImageData(qr.qr_data) ? (
                  <Image
                    src={qr.qr_data}
                    alt="QR tiket"
                    width={224}
                    height={224}
                    unoptimized
                    className="mx-auto h-56 w-56"
                  />
                ) : (
                  <div className="py-6">
                    <p className="text-xs font-semibold uppercase tracking-wide text-gray-500 mb-2">
                      QR Token (dynamic)
                    </p>
                    <code className="block break-all rounded bg-gray-100 p-3 text-xs text-gray-800">
                      {qr.qr_data}
                    </code>
                  </div>
                )
              ) : (
                <p className="py-16 text-gray-500">QR belum tersedia</p>
              )}

              {qr && (
                <p className="mt-3 text-sm text-gray-500">
                  &#8635; Refreshes in {secondsLeft ?? qr.refresh_in}s
                </p>
              )}
            </div>
            {qrError && (
              <Alert variant="warning" className="mt-3 text-left">
                {qrError}
              </Alert>
            )}
          </div>

          <div className="mt-6 bg-amber-50 border border-amber-200 rounded-lg p-4 text-left">
            <p className="text-xs font-bold uppercase tracking-wide text-amber-800">
              Fallback PIN
            </p>
            <p className="text-sm text-amber-700 mt-1">
              Jika QR gagal dipindai, gunakan PIN ini di gate.
            </p>
            <p className="mt-2 text-2xl font-mono font-bold tracking-widest text-amber-900">
              {ticket.fallback_pin || '------'}
            </p>
          </div>

          <div className="mt-6 space-y-3">
            <Button
              href={`/customer/resale/list/${ticket.id}`}
              variant="outline"
              className="w-full"
            >
              Jual Tiket (Resale)
            </Button>
            <Button href="/customer/my-tickets" className="w-full">
              Kembali ke Tiket Saya
            </Button>
          </div>
        </Card>
      </main>
    </div>
  );
}
