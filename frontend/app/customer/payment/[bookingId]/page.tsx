'use client';

import { Suspense, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useParams, useRouter, useSearchParams } from 'next/navigation';
import {
  apiClient,
  createIdempotencyKey,
  getApiErrorMessage,
} from '@/lib/api/client';
import { useAuthStore } from '@/lib/stores/authStore';
import Link from 'next/link';
import { Alert } from '@/components/ui/Alert';
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
  const [mockSubmitting, setMockSubmitting] = useState(false);
  const [mockError, setMockError] = useState('');

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

  // TEST_MODE only: the backend route does not exist outside it.
  const mockEnabled = process.env.NEXT_PUBLIC_TEST_MODE === 'true';

  const handleMockSuccess = async () => {
    setMockSubmitting(true);
    setMockError('');
    try {
      await apiClient.post('/webhook/payment/mock', { booking_id: bookingId });
      router.push('/customer/my-tickets');
    } catch (err) {
      setMockError(getApiErrorMessage(err, 'Mock payment gagal.'));
    } finally {
      setMockSubmitting(false);
    }
  };

  if (result) {
    return (
      <div className="min-h-screen bg-[#FAFAFA]">
        <div className="max-w-md mx-auto px-6 py-12">
          <div className="bg-white border border-[#E2E8F0] rounded-2xl p-6 shadow-sm">
            <h1 className="text-2xl font-bold text-[#171717] mb-2">
              Link pembayaran dibuat
            </h1>
            <p className="text-sm text-[#4D4D4D] mb-6">
              Status booking: {result.payment_status}. Tiket terbit otomatis
              setelah semua pembayaran terkonfirmasi.
            </p>

            <ul className="divide-y divide-[#E2E8F0]">
              {result.obligations.map((obligation, index) => (
                <li
                  key={`${obligation.user_id}-${index}`}
                  className="py-4 first:pt-0 last:pb-0"
                >
                  <div className="flex items-baseline justify-between gap-4">
                    <span className="text-sm font-medium text-[#171717]">
                      {obligation.seat_position
                        ? `Kursi ${obligation.seat_position}`
                        : 'Leader (bulk)'}
                    </span>
                    <span className="text-sm font-mono font-bold text-[#171717]">
                      {formatRupiah(obligation.amount)}
                    </span>
                  </div>
                  <p className="text-xs text-[#8F8F8F] mt-1">
                    User: {obligation.user_id} &bull; {obligation.status}
                  </p>
                  {obligation.payment_link && (
                    <a
                      href={obligation.payment_link}
                      target="_blank"
                      rel="noreferrer"
                      className="mt-1 inline-block text-sm text-[#D08080] hover:underline break-all"
                    >
                      {obligation.payment_link}
                    </a>
                  )}
                </li>
              ))}
            </ul>

            <div className="flex flex-col gap-3 mt-6">
              <Link
                href="/customer/my-tickets"
                className="w-full text-center bg-[#D08080] text-[#171717] font-medium py-3 rounded-lg hover:bg-[#c06b6b] transition-colors"
              >
                Lihat Tiket Saya
              </Link>
              <button
                type="button"
                onClick={() => router.push('/customer/dashboard')}
                className="w-full border border-[#E2E8F0] text-[#171717] font-medium py-3 rounded-lg hover:bg-[#FAFAFA] transition-colors"
              >
                Kembali ke Katalog
              </button>
              {mockEnabled && (
                <div className="pt-2 border-t border-[#E2E8F0]">
                  {mockError && (
                    <Alert variant="error" className="mb-3">
                      {mockError}
                    </Alert>
                  )}
                  <button
                    type="button"
                    onClick={handleMockSuccess}
                    disabled={mockSubmitting}
                    className="w-full text-xs font-medium text-[#8F8F8F] underline underline-offset-2 hover:text-[#171717] disabled:opacity-50"
                  >
                    {mockSubmitting
                      ? 'Memproses mock payment...'
                      : 'Simulate Payment Success (Mock)'}
                  </button>
                </div>
              )}
            </div>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="min-h-screen bg-[#FAFAFA]">
      <div className="max-w-md mx-auto px-6 py-12">
        <div className="bg-white border border-[#E2E8F0] rounded-2xl p-6 shadow-sm">
          <h1 className="text-2xl font-bold text-[#171717] text-center mb-6">
            Selesaikan Pembayaran
          </h1>

          <div className="text-center mb-6">
            <p className="text-sm text-[#4D4D4D]">
              {seats.length} tiket{sectionName ? ` - ${sectionName}` : ''}
            </p>
            <p className="text-3xl font-mono text-[#171717] font-bold mt-1">
              {formatRupiah(amount)}
            </p>
            {remaining !== null && (
              <p className="mt-2 text-sm font-medium text-[#B45309]">
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
              className={`w-full text-left p-4 border rounded-xl transition-colors ${
                mode === 'SPLIT'
                  ? 'border-[#D08080] bg-[#FAF2F2] ring-1 ring-[#D08080]'
                  : 'border-[#E2E8F0] bg-white hover:border-[#D08080]'
              }`}
            >
              <p className="font-semibold text-[#171717]">Split Pay</p>
              <p className="text-sm text-[#4D4D4D]">
                Setiap anggota-group membayar sendiri lewat link terpisah.
              </p>
            </button>
            <button
              type="button"
              onClick={() => setMode('BULK')}
              aria-pressed={mode === 'BULK'}
              className={`w-full text-left p-4 border rounded-xl transition-colors ${
                mode === 'BULK'
                  ? 'border-[#D08080] bg-[#FAF2F2] ring-1 ring-[#D08080]'
                  : 'border-[#E2E8F0] bg-white hover:border-[#D08080]'
              }`}
            >
              <p className="font-semibold text-[#171717]">Bulk Pay (Leader)</p>
              <p className="text-sm text-[#4D4D4D]">
                Leader membayar seluruh {formatRupiah(amount)} sekaligus.
              </p>
            </button>
          </div>

          {mode === 'SPLIT' && (
            <div className="mb-6">
              <p className="text-sm text-[#4D4D4D] mb-3">
                Masukkan user ID (UUID) pemilik tiap kursi. Kursi pertama sudah
                diisi dengan ID Anda sebagai leader.
              </p>
              <ul className="divide-y divide-[#E2E8F0]">
                {seats.map((seatPosition, index) => (
                  <li key={seatPosition} className="py-4 first:pt-0">
                    <div className="flex items-baseline justify-between gap-3 mb-2">
                      <label
                        htmlFor={`member-${seatPosition}`}
                        className="text-sm font-medium text-[#171717]"
                      >
                        User ID anggota kursi {seatPosition}
                      </label>
                      <span className="text-xs text-[#4D4D4D] shrink-0">
                        Kursi {seatPosition} &bull; {formatRupiah(price || amount)}
                      </span>
                    </div>
                    <input
                      id={`member-${seatPosition}`}
                      type="text"
                      placeholder="00000000-0000-0000-0000-000000000000"
                      value={memberIds[index] ?? ''}
                      onChange={(e) => setMemberId(index, e.target.value)}
                      className="w-full border border-[#E2E8F0] rounded-lg px-4 py-2.5 text-sm bg-white focus:outline-none focus:ring-2 focus:ring-[#D08080] focus:border-transparent transition-all"
                    />
                  </li>
                ))}
              </ul>
            </div>
          )}

          <button
            type="button"
            onClick={handlePay}
            disabled={
              remaining === 0 ||
              (mode === 'SPLIT' && (invalidMembers || seats.length === 0))
            }
            className="w-full bg-[#D08080] text-[#171717] font-medium py-3 rounded-lg mt-6 hover:bg-[#c06b6b] transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {submitting ? 'Memproses...' : 'Konfirmasi Pembayaran'}
          </button>
        </div>
      </div>
    </div>
  );
}

export default function PaymentPage() {
  return (
    <Suspense
      fallback={
        <div className="min-h-screen flex items-center justify-center bg-[#FAFAFA]">
          <p className="text-[#4D4D4D]">Memuat pembayaran...</p>
        </div>
      }
    >
      <PaymentContent />
    </Suspense>
  );
}
