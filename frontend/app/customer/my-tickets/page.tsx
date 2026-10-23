'use client';

import { useCallback, useEffect, useState } from 'react';
import { apiClient, getApiErrorMessage } from '@/lib/api/client';
import { Alert } from '@/components/ui/Alert';
import { Button } from '@/components/ui/Button';
import { Card } from '@/components/ui/Card';

interface TicketListItem {
  id: string;
  event_id: string;
  event_name: string;
  section_name: string;
  seat_position: number;
  status: string;
  fallback_pin: string;
  created_at: string;
}

interface TicketsPayload {
  tickets: TicketListItem[];
}

const STATUS_STYLE: Record<string, string> = {
  CONFIRMED: 'bg-green-100 text-green-800',
  PENDING: 'bg-amber-100 text-amber-800',
  CANCELLED: 'bg-gray-200 text-gray-600',
  REFUNDED: 'bg-blue-100 text-blue-700',
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
    <div className="min-h-screen bg-gray-50 text-gray-900">
      <header className="bg-white border-b border-gray-200">
        <div className="max-w-4xl mx-auto px-4 py-4 flex items-center justify-between">
          <Button href="/customer/dashboard" variant="outline">
            &larr; Katalog
          </Button>
          <Button variant="outline" onClick={() => void loadTickets()}>
            Muat Ulang
          </Button>
        </div>
      </header>

      <main className="max-w-4xl mx-auto px-4 py-8">
        <h1 className="text-3xl font-bold text-gray-900 mb-6">Tiket Saya</h1>

        {error && (
          <Alert variant="error" className="mb-6">
            {error}
          </Alert>
        )}

        {loading ? (
          <p className="text-gray-500 py-10 text-center">Memuat tiket...</p>
        ) : tickets.length === 0 ? (
          <Card className="text-center">
            <p className="text-gray-600 mb-4">
              Belum ada tiket. Pesan tiket dari katalog event.
            </p>
            <Button href="/customer/dashboard">Lihat Event</Button>
          </Card>
        ) : (
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            {tickets.map((ticket) => (
              <Card key={ticket.id}>
                <div className="flex items-start justify-between gap-3">
                  <h3 className="font-bold text-gray-900">
                    {ticket.event_name}
                  </h3>
                  <span
                    className={`text-xs font-semibold px-2 py-1 rounded ${
                      STATUS_STYLE[ticket.status] ?? 'bg-gray-100 text-gray-700'
                    }`}
                  >
                    {ticket.status}
                  </span>
                </div>
                <dl className="mt-3 space-y-1 text-sm text-gray-600">
                  <div className="flex justify-between">
                    <dt>Section</dt>
                    <dd className="font-medium text-gray-900">
                      {ticket.section_name}
                    </dd>
                  </div>
                  <div className="flex justify-between">
                    <dt>Kursi</dt>
                    <dd className="font-medium text-gray-900">
                      {ticket.seat_position}
                    </dd>
                  </div>
                  {ticket.fallback_pin && (
                    <div className="flex justify-between">
                      <dt>Fallback PIN</dt>
                      <dd className="font-mono font-medium text-gray-900">
                        {ticket.fallback_pin}
                      </dd>
                    </div>
                  )}
                  <div className="flex justify-between">
                    <dt>Diterbitkan</dt>
                    <dd className="font-medium text-gray-900">
                      {ticket.created_at
                        ? new Date(ticket.created_at).toLocaleDateString('id-ID')
                        : '-'}
                    </dd>
                  </div>
                </dl>
                <div className="mt-4">
                  <Button
                    href={`/customer/tickets/${ticket.id}`}
                    className="w-full"
                  >
                    Lihat Tiket
                  </Button>
                </div>
              </Card>
            ))}
          </div>
        )}
      </main>
    </div>
  );
}
