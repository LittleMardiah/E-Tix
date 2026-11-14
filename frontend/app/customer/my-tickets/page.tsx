'use client';

import { useCallback, useEffect, useState } from 'react';
import { apiClient, getApiErrorMessage } from '@/lib/api/client';
import Link from 'next/link';
import { Alert } from '@/components/ui/Alert';

interface TicketListItem {
  id: string;
  event_id: string;
  event_name: string;
  section_name: string;
  seat_position: number | null;
  status: string;
  fallback_pin: string;
  created_at: string;
}

interface TicketsPayload {
  tickets: TicketListItem[];
}

const STATUS_STYLE: Record<string, string> = {
  CONFIRMED: 'bg-green-100 text-green-700',
  USED: 'bg-gray-100 text-gray-700',
  RESALE_PENDING: 'bg-yellow-100 text-yellow-700',
  RESOLD: 'bg-yellow-100 text-yellow-700',
  PENDING_SPLIT_PAY: 'bg-amber-100 text-amber-800',
  CANCELLED: 'bg-gray-100 text-gray-700',
};

/**
 * My Tickets (HALAMAN 6.1).
 * Backed by GET /tickets (API CONTRACT §10.1).
 */
export default function MyTicketsPage() {
  const [tickets, setTickets] = useState<TicketListItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  const loadTickets = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const response = await apiClient.get<{ data: TicketsPayload }>('/tickets');
      setTickets(response.data.data.tickets ?? []);
    } catch (err) {
      setError(getApiErrorMessage(err, 'Gagal memuat tiket'));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadTickets();
  }, [loadTickets]);

  return (
    <div className="max-w-6xl mx-auto px-6 py-12">
      <div className="flex flex-wrap items-center justify-between gap-4 mb-8">
        <h1 className="text-3xl font-bold tracking-tight text-[#171717]">
          My Tickets
        </h1>
        <button
          type="button"
          onClick={() => void loadTickets()}
          className="inline-flex h-10 items-center justify-center rounded-lg border border-[#E2E8F0] bg-white px-4 text-sm font-medium text-[#171717] transition-all duration-200 hover:bg-[#FAFAFA]"
        >
          Muat Ulang
        </button>
      </div>

      {error && (
        <Alert variant="error" className="mb-6">
          {error}
        </Alert>
      )}

      {loading ? (
        <p className="text-[#4D4D4D] py-10 text-center">Memuat tiket...</p>
      ) : tickets.length === 0 ? (
        <div className="bg-white border border-[#E2E8F0] rounded-xl p-10 text-center">
          <svg
            aria-hidden="true"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth={1.5}
            className="mx-auto mb-4 h-12 w-12 text-[#D08080]"
          >
            <path
              strokeLinecap="round"
              strokeLinejoin="round"
              d="M16.5 6v.75m0 3v.75m0 3v.75m0 3V18m-9-12v.75m0 3v.75m0 3v.75m0 3V18M3.375 6h17.25c.621 0 1.125.504 1.125 1.125v13.5c0 .621-.504 1.125-1.125 1.125H3.375A1.125 1.125 0 0 1 2.25 20.625v-13.5c0-.621.504-1.125 1.125-1.125Z"
            />
          </svg>
          <p className="text-[#4D4D4D] mb-4">
            Belum ada tiket. Pesan tiket dari katalog event.
          </p>
          <Link
            href="/customer/dashboard"
            className="inline-flex items-center justify-center rounded-lg border border-[#D08080] px-4 py-2.5 text-sm font-medium text-[#D08080] transition-colors hover:bg-[#D08080] hover:text-[#171717]"
          >
            Lihat Event
          </Link>
        </div>
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
          {tickets.map((ticket) => (
            <div
              key={ticket.id}
              className="bg-white border border-[#E2E8F0] rounded-xl p-6 hover:border-[#D08080] hover:shadow-sm transition-all duration-200 flex flex-col"
            >
              <div className="flex items-start justify-between gap-3">
                <h2 className="font-bold text-lg text-[#171717]">
                  {ticket.event_name}
                </h2>
                <span
                  className={`shrink-0 text-xs px-2.5 py-1 rounded-full font-medium ${
                    STATUS_STYLE[ticket.status] ?? 'bg-gray-100 text-gray-700'
                  }`}
                >
                  {ticket.status}
                </span>
              </div>

              <dl className="mt-4 space-y-2 text-sm flex-1">
                <div className="flex justify-between gap-3">
                  <dt className="text-[#4D4D4D]">Section</dt>
                  <dd className="font-medium text-[#171717]">
                    {ticket.section_name}
                  </dd>
                </div>
                <div className="flex justify-between gap-3">
                  <dt className="text-[#4D4D4D]">Kursi</dt>
                  <dd className="font-medium text-[#171717] font-[Inter]">
                    {ticket.seat_position ?? 'General Admission (ZONED)'}
                  </dd>
                </div>
                {ticket.fallback_pin && (
                  <div className="flex justify-between gap-3">
                    <dt className="text-[#4D4D4D]">Fallback PIN</dt>
                    <dd className="font-mono font-medium text-[#171717]">
                      {ticket.fallback_pin}
                    </dd>
                  </div>
                )}
                <div className="flex justify-between gap-3">
                  <dt className="text-[#4D4D4D]">Diterbitkan</dt>
                  <dd className="font-medium text-[#171717]">
                    {ticket.created_at
                      ? new Date(ticket.created_at).toLocaleDateString('id-ID')
                      : '-'}
                  </dd>
                </div>
              </dl>

              <div className="mt-6">
                <Link
                  href={`/customer/tickets/${ticket.id}`}
                  className="text-[#D08080] font-medium hover:underline"
                >
                  View Ticket
                </Link>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
