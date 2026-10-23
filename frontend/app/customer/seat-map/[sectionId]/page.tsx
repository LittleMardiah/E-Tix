'use client';

import { Suspense, useCallback, useEffect, useMemo, useState } from 'react';
import { useParams, useRouter, useSearchParams } from 'next/navigation';
import {
  apiClient,
  createIdempotencyKey,
  getApiErrorMessage,
} from '@/lib/api/client';
import { Alert } from '@/components/ui/Alert';
import { Button } from '@/components/ui/Button';
import { Card } from '@/components/ui/Card';
import { formatRupiah, parseAmount, seatLabel } from '@/lib/format';

interface SeatStatus {
  position: number;
  status: 'AVAILABLE' | 'TAKEN' | 'LOCKED';
}

interface SeatAvailability {
  event_id: string;
  section_id: string;
  section_name: string;
  layout_version: number;
  total_seats: number;
  available_seats: number;
  seats: SeatStatus[];
}

interface SectionDetail {
  section_id: string;
  name: string;
  price: string;
}

interface EventDetail {
  event_id: string;
  name: string;
  sections: SectionDetail[];
}

interface LockSeatsResponse {
  booking_id: string;
  event_id: string;
  section_id: string;
  seat_positions: number[];
  total_seats: number;
  total_amount: string;
  payment_mode: string;
  payment_status: string;
  expires_at: string;
}

type PaymentMode = 'SPLIT' | 'BULK';

/** Backend limit: booking.MaxGroupSeats (backend/internal/booking/model.go). */
const MAX_GROUP_SEATS = 10;

const QUEUE_TOKEN_STORAGE_KEY = 'e-tix-flash:queue-token';

function readStoredQueueToken(eventId: string): string | null {
  if (typeof window === 'undefined') return null;
  return window.sessionStorage.getItem(`${QUEUE_TOKEN_STORAGE_KEY}:${eventId}`);
}

/**
 * Seat Map (HALAMAN 4.1).
 * Backed by GET /events/{eid}/sections/{sid}/seats (API CONTRACT §8.3) and
 * POST /events/{eid}/sections/{sid}/seats (§9.1, atomic bitmask lock).
 */
function SeatMapContent() {
  const params = useParams<{ sectionId: string }>();
  const searchParams = useSearchParams();
  const router = useRouter();

  const sectionId = params?.sectionId ?? '';
  const eventId = searchParams.get('eventId') ?? '';
  const queueTokenParam = searchParams.get('queueToken');

  const [seats, setSeats] = useState<SeatStatus[]>([]);
  const [sectionName, setSectionName] = useState('');
  const [eventName, setEventName] = useState('');
  const [seatPrice, setSeatPrice] = useState<string | null>(null);
  const [selected, setSelected] = useState<number[]>([]);
  const [mode, setMode] = useState<PaymentMode>('SPLIT');
  const [loading, setLoading] = useState(true);
  const [locking, setLocking] = useState(false);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');

  const loadSeats = useCallback(async () => {
    if (!eventId || !sectionId) return;
    try {
      const response = await apiClient.get<{ data: SeatAvailability }>(
        `/events/${eventId}/sections/${sectionId}/seats`
      );
      setSeats(response.data.data.seats ?? []);
      setSectionName(response.data.data.section_name ?? '');
      setError('');
    } catch (err) {
      setError(getApiErrorMessage(err, 'Gagal memuat denah kursi'));
    } finally {
      setLoading(false);
    }
  }, [eventId, sectionId]);

  useEffect(() => {
    if (!eventId || !sectionId) return;
    void loadSeats();
  }, [eventId, sectionId, loadSeats]);

  useEffect(() => {
    if (!eventId) return;
    let active = true;
    apiClient
      .get<{ data: EventDetail }>(`/events/${eventId}`)
      .then((response) => {
        if (!active) return;
        const data = response.data.data;
        setEventName(data.name ?? '');
        const section = data.sections?.find(
          (item) => item.section_id === sectionId
        );
        setSeatPrice(section?.price ?? null);
      })
      .catch(() => undefined);
    return () => {
      active = false;
    };
  }, [eventId, sectionId]);

  const queueToken = useMemo(() => {
    const stored = readStoredQueueToken(eventId);
    return stored || queueTokenParam;
  }, [eventId, queueTokenParam]);

  const toggleSeat = (seat: SeatStatus) => {
    if (seat.status !== 'AVAILABLE') return;
    setNotice('');
    setSelected((prev) => {
      if (prev.includes(seat.position)) {
        return prev.filter((position) => position !== seat.position);
      }
      if (prev.length >= MAX_GROUP_SEATS) {
        setNotice(`Maksimal ${MAX_GROUP_SEATS} kursi per transaksi.`);
        return prev;
      }
      return [...prev, seat.position].sort((a, b) => a - b);
    });
  };

  const seatClass = (seat: SeatStatus) => {
    if (selected.includes(seat.position)) {
      return 'bg-blue-600 text-white hover:bg-blue-700';
    }
    if (seat.status === 'AVAILABLE') {
      return 'bg-green-100 text-green-800 hover:bg-green-200';
    }
    if (seat.status === 'LOCKED') {
      return 'bg-yellow-200 text-yellow-900 cursor-not-allowed';
    }
    return 'bg-gray-200 text-gray-400 cursor-not-allowed';
  };

  const handleLockSeats = async () => {
    if (selected.length === 0) return;
    setLocking(true);
    setError('');
    setNotice('');
    try {
      const response = await apiClient.post<{ data: LockSeatsResponse }>(
        `/events/${eventId}/sections/${sectionId}/seats`,
        { seat_positions: selected, payment_mode: mode },
        {
          headers: {
            'X-Queue-Token': queueToken ?? '',
            'X-Idempotency-Key': createIdempotencyKey(),
          },
        }
      );
      const booking = response.data.data;
      const perSeat = perSeatPrice(booking.total_amount, booking.total_seats);
      const query = new URLSearchParams({
        amount: booking.total_amount,
        price: perSeat,
        seats: booking.seat_positions.join(','),
        mode: booking.payment_mode,
        section: sectionName,
        expires: booking.expires_at,
      });
      router.push(`/customer/payment/${booking.booking_id}?${query.toString()}`);
    } catch (err: unknown) {
      const status = (err as { response?: { status?: number } })?.response
        ?.status;
      if (status === 409) {
        setSelected([]);
        setNotice(
          'Ada kursi yang baru saja diambil pembeli lain. Pilih ulang kursi Anda.'
        );
        await loadSeats();
      } else if (status === 401) {
        setError(
          'Token antrean tidak valid atau sudah dipakai. Kembali ke waiting room.'
        );
      } else {
        setError(getApiErrorMessage(err, 'Gagal mengunci kursi'));
      }
    } finally {
      setLocking(false);
    }
  };

  const totalPrice = seatPrice
    ? parseAmount(seatPrice) * selected.length
    : null;

  return (
    <div className="min-h-screen bg-gray-50 text-gray-900">
      <header className="bg-white border-b border-gray-200">
        <div className="max-w-4xl mx-auto px-4 py-4 flex items-center justify-between">
          <Button href={`/customer/events/${eventId}`} variant="outline">
            &larr; Kembali
          </Button>
          <p className="text-sm font-medium text-gray-700">
            {eventName}
            {sectionName ? ` - ${sectionName}` : ''}
          </p>
        </div>
      </header>

      <main className="max-w-4xl mx-auto px-4 py-8">
        <h1 className="text-2xl font-bold mb-1">Pilih Kursi</h1>
        <p className="text-sm text-gray-500 mb-6">
          Maksimal {MAX_GROUP_SEATS} kursi per transaksi. Kursi dikunci 3 menit
          setelah tombol ditekan.
        </p>

        {error && (
          <Alert variant="error" className="mb-4">
            {error}
          </Alert>
        )}
        {notice && (
          <Alert variant="warning" className="mb-4">
            {notice}
          </Alert>
        )}
        {!queueToken && (
          <Alert variant="warning" className="mb-4">
            Token antrean tidak ditemukan. Kembali ke waiting room untuk
            mengambil token baru.
          </Alert>
        )}

        <div className="flex flex-wrap items-center gap-4 mb-6 text-xs text-gray-600">
          <span className="flex items-center gap-1">
            <span className="w-4 h-4 rounded bg-green-100 border border-green-300" />
            Tersedia
          </span>
          <span className="flex items-center gap-1">
            <span className="w-4 h-4 rounded bg-blue-600" />
            Dipilih
          </span>
          <span className="flex items-center gap-1">
            <span className="w-4 h-4 rounded bg-yellow-200 border border-yellow-300" />
            Dikunci pembeli lain
          </span>
          <span className="flex items-center gap-1">
            <span className="w-4 h-4 rounded bg-gray-200" />
            Terjual
          </span>
        </div>

        <Card className="mb-6">
          {loading ? (
            <p className="text-center text-gray-500 py-8">Memuat kursi...</p>
          ) : (
            <div className="grid grid-cols-6 sm:grid-cols-10 gap-2">
              {seats.map((seat) => (
                <button
                  key={seat.position}
                  type="button"
                  onClick={() => toggleSeat(seat)}
                  disabled={seat.status !== 'AVAILABLE'}
                  aria-pressed={selected.includes(seat.position)}
                  aria-label={`Kursi ${seatLabel(seat.position)} - ${seat.status}`}
                  className={`py-2 rounded text-xs font-semibold transition-colors ${seatClass(seat)}`}
                >
                  {seatLabel(seat.position)}
                </button>
              ))}
            </div>
          )}
        </Card>

        <Card className="mb-6">
          <h2 className="font-semibold text-gray-900 mb-3">
            Metode pembayaran
          </h2>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
            {(
              [
                {
                  value: 'SPLIT' as PaymentMode,
                  title: 'Split Pay',
                  desc: 'Setiap anggota bayar sendiri-sendiri via link.',
                },
                {
                  value: 'BULK' as PaymentMode,
                  title: 'Bulk Pay',
                  desc: 'Leader membayar seluruh kursi sekaligus.',
                },
              ]
            ).map((option) => (
              <button
                key={option.value}
                type="button"
                onClick={() => setMode(option.value)}
                aria-pressed={mode === option.value}
                className={`text-left p-4 rounded-lg border transition-colors ${
                  mode === option.value
                    ? 'border-blue-600 bg-blue-50 ring-1 ring-blue-600'
                    : 'border-gray-200 bg-white hover:border-gray-300'
                }`}
              >
                <p className="font-semibold text-gray-900">{option.title}</p>
                <p className="text-sm text-gray-500 mt-1">{option.desc}</p>
              </button>
            ))}
          </div>
        </Card>

        <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4">
          <div>
            <p className="text-sm text-gray-600">
              Terpilih:{' '}
              <span className="font-semibold text-gray-900">
                {selected.length ? selected.join(', ') : '-'}
              </span>
            </p>
            {totalPrice !== null && selected.length > 0 && (
              <p className="text-sm text-gray-600">
                Total sementara:{' '}
                <span className="font-semibold text-gray-900">
                  {formatRupiah(totalPrice)}
                </span>
              </p>
            )}
          </div>
          <Button
            onClick={handleLockSeats}
            isLoading={locking}
            disabled={selected.length === 0 || !queueToken}
          >
            Kunci {selected.length || ''} Kursi &amp; Lanjut
          </Button>
        </div>
      </main>
    </div>
  );
}

/**
 * The booking total is section_price x seat_count
 * (backend/internal/booking/service.go), so the per-seat share is derived
 * with integer cents to avoid floating point artefacts.
 */
function perSeatPrice(totalAmount: string, seatCount: number): string {
  if (!seatCount) return '0.00';
  const totalCents = Math.round(parseAmount(totalAmount) * 100);
  const seatCents = Math.round(totalCents / seatCount);
  return (seatCents / 100).toFixed(2);
}

export default function SeatMapPage() {
  return (
    <Suspense
      fallback={
        <div className="min-h-screen flex items-center justify-center bg-gray-50">
          <p className="text-gray-500">Memuat denah kursi...</p>
        </div>
      }
    >
      <SeatMapContent />
    </Suspense>
  );
}
