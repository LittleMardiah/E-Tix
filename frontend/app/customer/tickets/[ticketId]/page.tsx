'use client';

import { useCallback, useEffect, useState } from 'react';
import Image from 'next/image';
import Link from 'next/link';
import { useParams } from 'next/navigation';
import { apiClient, getApiErrorMessage } from '@/lib/api/client';
import { Alert } from '@/components/ui/Alert';

interface TicketDetail {
  id: string;
  event_id: string;
  event_name: string;
  section_id: string;
  section_name: string;
  seat_position: number | null;
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
      <div className="max-w-md mx-auto px-6 py-12">
        <p className="text-[#4D4D4D] py-10 text-center">Memuat tiket...</p>
      </div>
    );
  }

  if (error || !ticket) {
    return (
      <div className="max-w-md mx-auto px-6 py-12">
        <Alert variant="error">{error || 'Tiket tidak ditemukan'}</Alert>
        <Link
          href="/customer/my-tickets"
          className="mt-4 inline-block text-sm font-medium text-[#D08080] hover:underline"
        >
          &larr; Tiket Saya
        </Link>
      </div>
    );
  }

  const statusStyle =
    ticket.status === 'CONFIRMED'
      ? 'bg-green-100 text-green-700'
      : ticket.status === 'RESALE_PENDING' || ticket.status === 'RESOLD'
        ? 'bg-yellow-100 text-yellow-700'
        : 'bg-gray-100 text-gray-700';

  return (
    <div className="max-w-md mx-auto px-6 py-12">
      <Link
        href="/customer/my-tickets"
        className="text-sm text-[#4D4D4D] hover:text-[#171717] transition-colors"
      >
        &larr; Tiket Saya
      </Link>

      <div className="bg-white border border-[#E2E8F0] rounded-2xl p-8 text-center shadow-sm mt-4">
        <span
          className={`text-xs px-2.5 py-1 rounded-full font-medium ${statusStyle}`}
        >
          {ticket.status}
        </span>

        <h1 className="font-bold text-xl text-[#171717] mt-3">
          {ticket.event_name}
        </h1>
        <p className="text-sm text-[#4D4D4D] mt-1 font-[Inter]">
          {ticket.seat_position === null || ticket.seat_position === undefined
            ? `${ticket.section_name} — General Admission (ZONED)`
            : `${ticket.section_name} — Seat ${ticket.seat_position}`}
        </p>

        <div className="border-2 border-dashed border-[#E2E8F0] rounded-xl p-6 my-6 flex justify-center">
          <div className="w-full">
            <p className="text-xs font-semibold uppercase tracking-wide text-[#4D4D4D] mb-3">
              QR Code
            </p>
            {qrLoading && !qr ? (
              <p className="py-16 text-[#4D4D4D]">Memuat QR...</p>
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
                  <p className="text-xs font-semibold uppercase tracking-wide text-[#4D4D4D] mb-2">
                    QR Token (dynamic)
                  </p>
                  <code className="block break-all rounded-lg bg-[#FAFAFA] border border-[#E2E8F0] p-3 text-xs text-[#171717]">
                    {qr.qr_data}
                  </code>
                </div>
              )
            ) : (
              <p className="py-16 text-[#4D4D4D]">QR belum tersedia</p>
            )}

            {qr && (
              <p className="mt-3 text-sm text-[#4D4D4D]">
                &#8635; Refreshes in {secondsLeft ?? qr.refresh_in}s
              </p>
            )}
          </div>
        </div>

        {qrError && (
          <Alert variant="warning" className="text-left">
            {qrError}
          </Alert>
        )}

        <div className="bg-[#FAFAFA] border border-[#E2E8F0] rounded-xl p-4 mt-6 text-left">
          <p className="text-xs font-bold uppercase tracking-wide text-[#4D4D4D]">
            Fallback PIN
          </p>
          <p className="text-sm text-[#4D4D4D] mt-1">
            Jika QR gagal dipindai, gunakan PIN ini di gate.
          </p>
          <p className="mt-2 text-2xl font-mono font-bold tracking-widest text-[#171717]">
            {ticket.fallback_pin || '------'}
          </p>
        </div>

        <Link
          href={`/customer/resale/list/${ticket.id}`}
          className="block w-full border border-[#D08080] text-[#D08080] py-3 rounded-lg mt-6 hover:bg-[#D08080] hover:text-[#171717] transition-colors"
        >
          Jual Tiket (Resale)
        </Link>
        <Link
          href="/customer/my-tickets"
          className="block w-full text-[#4D4D4D] text-sm py-3 mt-1 hover:text-[#171717] transition-colors"
        >
          Kembali ke Tiket Saya
        </Link>
      </div>
    </div>
  );
}
