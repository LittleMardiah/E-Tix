'use client';

import { Suspense, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useParams, useRouter, useSearchParams } from 'next/navigation';
import {
  apiClient,
  createIdempotencyKey,
  getApiErrorMessage,
} from '@/lib/api/client';
import { useAuthStore } from '@/lib/stores/authStore';
import { Alert } from '@/components/ui/Alert';
import { Button } from '@/components/ui/Button';
import { Card } from '@/components/ui/Card';
import { Input } from '@/components/ui/Input';
import { formatRupiah } from '@/lib/format';

interface Obligation {
  user_id: string;
  seat_position?: number;
  amount: string;
  status: string;
  payment_link?: string;
  external_transaction_id?: string;
}

interface InitiatePaymentResponse {
  booking_id: string;
  payment_status: string;
  obligations: Obligation[];
  expires_at: string;
}

type PaymentMode = 'SPLIT' | 'BULK';

const UUID_PATTERN =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/**
 * Payment Mode (HALAMAN 5.1) and the resulting payment links (HALAMAN 5.2).
 * Backed by POST /bookings/{booking_id}/payments (API CONTRACT §9.2).
 */
function PaymentContent() {
  const params = useParams<{ bookingId: string }>();
  const searchParams = useSearchParams();
  const router = useRouter();
  const storeUser = useAuthStore((state) => state.user);

  const bookingId = params?.bookingId ?? '';
  const amount = searchParams.get('amount') ?? '';
  const price = searchParams.get('price') ?? '';
  const seatsParam = searchParams.get('seats') ?? '';
  const sectionName = searchParams.get('section') ?? '';
  const expiresAt = searchParams.get('expires') ?? '';
  const lockedMode = searchParams.get('mode') === 'BULK' ? 'BULK' : 'SPLIT';

  const seats = useMemo(
    () =>
      seatsParam
        .split(',')
        .map((value) => Number(value))
        .filter((value) => Number.isFinite(value) && value > 0),
    [seatsParam]
  );

  const [mode, setMode] = useState<PaymentMode>(lockedMode);
  const [memberIds, setMemberIds] = useState<string[]>([]);
  const [leaderId, setLeaderId] = useState(storeUser?.user_id ?? '');
  const [result, setResult] = useState<InitiatePaymentResponse | null>(null);
  const [error, setError] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [remaining, setRemaining] = useState<number | null>(null);

  const prefilledRef = useRef(false);

  useEffect(() => {
    setMemberIds((prev) =>
      prev.length === seats.length ? prev : Array(seats.length).fill('')
    );
  }, [seats.length]);

  useEffect(() => {
    if (storeUser?.user_id) {
      setLeaderId(storeUser.user_id);
      return;
    }
    let active = true;
    apiClient
      .get<{ data: { user_id: string } }>('/users/me')
      .then((response) => {
        if (active) setLeaderId(response.data.data.user_id);
      })
      .catch(() => undefined);
    return () => {
      active = false;
    };
  }, [storeUser?.user_id]);

  useEffect(() => {
    if (prefilledRef.current || !leaderId || seats.length === 0) return;
    prefilledRef.current = true;
    setMemberIds((prev) =>
      prev.map((value, index) => (index === 0 && !value ? leaderId : value))
    );
  }, [leaderId, seats.length]);

  useEffect(() => {
    if (!expiresAt) return;
    const deadline = new Date(expiresAt).getTime();
    if (Number.isNaN(deadline)) return;
    const tick = () =>
      setRemaining(Math.max(0, Math.floor((deadline - Date.now()) / 1000)));
    tick();
    const timer = setInterval(tick, 1000);
    return () => clearInterval(timer);
  }, [expiresAt]);

  const setMemberId = useCallback((index: number, value: string) => {
    setMemberIds((prev) => {
      const next = [...prev];
      next[index] = value;
      return next;
    });
  }, []);

  const invalidMembers = useMemo(
    () =>
      memberIds.some(
        (value) => !value || !UUID_PATTERN.test(value.trim())
      ),
    [memberIds]
  );

  const handlePay = async () => {
    setSubmitting(true);
    setError('');
    try {
      const body =
        mode === 'BULK'
          ? {
              payment_mode: 'BULK',
              leader_pays_all: true,
              amount,
            }
          : {
              payment_mode: 'SPLIT',
              members: seats.map((seatPosition, index) => ({
                user_id: memberIds[index]?.trim(),
                seat_position: seatPosition,
                amount: price || amount,
              })),
            };

      const response = await apiClient.post<{ data: InitiatePaymentResponse }>(
        `/bookings/${bookingId}/payments`,
        body,
        { headers: { 'X-Idempotency-Key': createIdempotencyKey() } }
      );
      setResult(response.data.data);
    } catch (err) {
      setError(getApiErrorMessage(err, 'Gagal inisiasi pembayaran'));
    } finally {
      setSubmitting(false);
    }
  };

  if (result) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-gray-50 px-4">
        <Card className="w-full max-w-lg">
          <h2 className="text-2xl font-bold text-gray-900 text-center mb-2">
            Link pembayaran dibuat
          </h2>
          <p className="text-center text-sm text-gray-500 mb-6">
            Status booking: {result.payment_status}. Tiket terbit otomatis
            setelah semua pembayaran terkonfirmasi.
          </p>

          <div className="space-y-3">
            {result.obligations.map((obligation, index) => (
              <div
                key={`${obligation.user_id}-${index}`}
                className="border border-gray-200 rounded-lg p-3"
              >
                <div className="flex justify-between text-sm">
                  <span className="font-medium text-gray-900">
                    {obligation.seat_position
                      ? `Kursi ${obligation.seat_position}`
                      : 'Leader (bulk)'}
                  </span>
                  <span className="font-semibold">
                    {formatRupiah(obligation.amount)}
                  </span>
                </div>
                <p className="text-xs text-gray-500 mt-1">
                  User: {obligation.user_id} &bull; {obligation.status}
                </p>
                {obligation.payment_link && (
                  <a
                    href={obligation.payment_link}
                    target="_blank"
                    rel="noreferrer"
                    className="text-sm text-blue-600 hover:underline break-all"
                  >
                    {obligation.payment_link}
                  </a>
                )}
              </div>
            ))}
          </div>

          <div className="flex gap-3 mt-6">
            <Button
              href="/customer/my-tickets"
              className="flex-1"
            >
              Lihat Tiket Saya
            </Button>
            <Button
              variant="outline"
              className="flex-1"
              onClick={() => router.push('/customer/dashboard')}
            >
              Kembali ke Katalog
            </Button>
          </div>
        </Card>
      </div>
    );
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-gray-50 px-4">
      <Card className="w-full max-w-lg">
        <h2 className="text-2xl font-bold text-gray-900 text-center mb-4">
          Pilih Metode Pembayaran
        </h2>

        <div className="bg-gray-50 rounded-lg p-4 mb-5 text-sm text-gray-700">
          <p className="font-semibold text-gray-900">
            {seats.length} tiket{sectionName ? ` - ${sectionName}` : ''}
          </p>
          <p className="mt-1">
            Total:{' '}
            <span className="font-semibold text-gray-900">
              {formatRupiah(amount)}
            </span>
          </p>
          {remaining !== null && (
            <p className="mt-1 text-amber-700 font-medium">
              Kursi dilepas dalam {Math.floor(remaining / 60)}:
              {String(remaining % 60).padStart(2, '0')}
            </p>
          )}
        </div>

        {error && (
          <Alert variant="error" className="mb-4">
            {error}
          </Alert>
        )}

        <div className="space-y-3 mb-6">
          <button
            type="button"
            onClick={() => setMode('SPLIT')}
            aria-pressed={mode === 'SPLIT'}
            className={`w-full text-left p-4 border rounded-lg transition-colors ${
              mode === 'SPLIT'
                ? 'border-blue-600 bg-blue-50 ring-1 ring-blue-600'
                : 'border-gray-200 bg-white hover:border-gray-300'
            }`}
          >
            <p className="font-semibold text-gray-900">Split Pay</p>
            <p className="text-sm text-gray-500">
              Setiap anggota-group membayar sendiri lewat link terpisah.
            </p>
          </button>
          <button
            type="button"
            onClick={() => setMode('BULK')}
            aria-pressed={mode === 'BULK'}
            className={`w-full text-left p-4 border rounded-lg transition-colors ${
              mode === 'BULK'
                ? 'border-blue-600 bg-blue-50 ring-1 ring-blue-600'
                : 'border-gray-200 bg-white hover:border-gray-300'
            }`}
          >
            <p className="font-semibold text-gray-900">Bulk Pay (Leader)</p>
            <p className="text-sm text-gray-500">
              Leader membayar seluruh {formatRupiah(amount)} sekaligus.
            </p>
          </button>
        </div>

        {mode === 'SPLIT' && (
          <div className="space-y-3 mb-6">
            <p className="text-sm text-gray-600">
              Masukkan user ID (UUID) pemilik tiap kursi. Kursi pertama sudah
              diisi dengan ID Anda sebagai leader.
            </p>
            {seats.map((seatPosition, index) => (
              <div key={seatPosition} className="flex items-end gap-3">
                <div className="w-28 shrink-0 text-sm text-gray-600 pb-2">
                  Kursi {seatPosition} &bull;{' '}
                  {formatRupiah(price || amount)}
                </div>
                <div className="flex-1">
                  <Input
                    label={`User ID anggota kursi ${seatPosition}`}
                    placeholder="00000000-0000-0000-0000-000000000000"
                    value={memberIds[index] ?? ''}
                    onChange={(e) => setMemberId(index, e.target.value)}
                  />
                </div>
              </div>
            ))}
          </div>
        )}

        <Button
          className="w-full"
          onClick={handlePay}
          isLoading={submitting}
          disabled={
            remaining === 0 ||
            (mode === 'SPLIT' && (invalidMembers || seats.length === 0))
          }
        >
          Konfirmasi Pembayaran
        </Button>
      </Card>
    </div>
  );
}

export default function PaymentPage() {
  return (
    <Suspense
      fallback={
        <div className="min-h-screen flex items-center justify-center bg-gray-50">
          <p className="text-gray-500">Memuat pembayaran...</p>
        </div>
      }
    >
      <PaymentContent />
    </Suspense>
  );
}
