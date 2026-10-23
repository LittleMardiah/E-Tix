'use client';

import { useCallback, useEffect, useState } from 'react';
import { useRouter } from 'next/navigation';
import { apiClient, getApiErrorMessage } from '@/lib/api/client';
import { useAuthStore } from '@/lib/stores/authStore';
import { Alert } from '@/components/ui/Alert';
import { Button } from '@/components/ui/Button';
import { Card } from '@/components/ui/Card';
import { Input } from '@/components/ui/Input';
import { formatRupiah } from '@/lib/format';

interface SectionBrief {
  section_id: string;
  name: string;
  price: string;
}

interface EventSummary {
  event_id: string;
  name: string;
  description: string;
  venue_name: string;
  venue_address: string;
  event_date: string;
  event_start_time: string;
  status: string;
  min_price: string;
  max_price: string;
  tickets_available: number;
  total_tickets: number;
  sections: SectionBrief[];
}

interface EventsPayload {
  events: EventSummary[];
}

/**
 * Event Catalog (HALAMAN 2.1) — the landing screen of the customer app.
 * Backed by GET /events (API CONTRACT §8.1, public catalog).
 */
export default function CustomerDashboard() {
  const router = useRouter();
  const user = useAuthStore((state) => state.user);
  const logout = useAuthStore((state) => state.logout);

  const [events, setEvents] = useState<EventSummary[]>([]);
  const [search, setSearch] = useState('');
  const [query, setQuery] = useState('');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  const loadEvents = useCallback(async (keyword: string) => {
    setLoading(true);
    setError('');
    try {
      const params: Record<string, string> = { status: 'ON_SALE', limit: '20' };
      if (keyword.trim()) params.search = keyword.trim();
      const response = await apiClient.get<{ data: EventsPayload }>('/events', {
        params,
      });
      setEvents(response.data.data.events ?? []);
    } catch (err) {
      setError(getApiErrorMessage(err, 'Gagal memuat daftar event'));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadEvents(query);
  }, [loadEvents, query]);

  const handleLogout = () => {
    logout();
    router.push('/auth/login');
  };

  return (
    <div className="min-h-screen bg-gray-50 text-gray-900">
      <header className="bg-white border-b border-gray-200">
        <div className="max-w-6xl mx-auto px-4 py-4 flex items-center justify-between gap-4">
          <div>
            <h1 className="text-xl font-bold">E-Tix Flash</h1>
            <p className="text-sm text-gray-500">
              {user ? `Halo, ${user.name}` : 'Event Catalog'}
            </p>
          </div>
          <div className="flex items-center gap-2">
            <Button href="/customer/my-tickets" variant="outline">
              Tiket Saya
            </Button>
            <Button variant="outline" onClick={handleLogout}>
              Logout
            </Button>
          </div>
        </div>
      </header>

      <main className="max-w-6xl mx-auto px-4 py-8">
        <h2 className="text-3xl font-bold mb-2">Upcoming Events</h2>
        <p className="text-gray-600 mb-6">
          Pilih event, pilih section, lalu kunci kursi Anda.
        </p>

        <form
          className="flex flex-col sm:flex-row gap-3 sm:items-end mb-6"
          onSubmit={(e) => {
            e.preventDefault();
            setQuery(search);
          }}
        >
          <div className="flex-1">
            <Input
              label="Cari event"
              placeholder="DEWA 19, festival, ..."
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
          </div>
          <Button type="submit" variant="outline">
            Cari
          </Button>
        </form>

        {error && (
          <Alert variant="error" className="mb-6">
            {error}
          </Alert>
        )}

        {loading ? (
          <p className="text-gray-500 py-10 text-center">Memuat event...</p>
        ) : events.length === 0 ? (
          <Card className="text-center">
            <p className="text-gray-600">
              Belum ada event yang dijual saat ini.
            </p>
          </Card>
        ) : (
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
            {events.map((event) => (
              <Card key={event.event_id} className="flex flex-col">
                <div className="flex-1">
                  <h3 className="text-lg font-bold text-gray-900 mb-1">
                    {event.name}
                  </h3>
                  <p className="text-sm text-gray-500">
                    {event.venue_name} &bull; {event.event_date} &bull;{' '}
                    {event.event_start_time?.slice(0, 5)}
                  </p>
                  <p className="text-sm text-gray-600 mt-3 line-clamp-3">
                    {event.description}
                  </p>
                  <dl className="mt-4 space-y-1 text-sm">
                    <div className="flex justify-between">
                      <dt className="text-gray-500">Mulai dari</dt>
                      <dd className="font-semibold">
                        {formatRupiah(event.min_price)}
                      </dd>
                    </div>
                    <div className="flex justify-between">
                      <dt className="text-gray-500">Sisa tiket</dt>
                      <dd className="font-semibold">
                        {event.tickets_available} / {event.total_tickets}
                      </dd>
                    </div>
                    <div className="flex justify-between">
                      <dt className="text-gray-500">Section</dt>
                      <dd className="font-semibold">
                        {event.sections?.length ?? 0}
                      </dd>
                    </div>
                  </dl>
                </div>
                <Button
                  href={`/customer/events/${event.event_id}`}
                  className="mt-5 w-full"
                >
                  Lihat Detail
                </Button>
              </Card>
            ))}
          </div>
        )}
      </main>
    </div>
  );
}
