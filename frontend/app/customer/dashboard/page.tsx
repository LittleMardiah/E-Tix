'use client';

import { useCallback, useEffect, useState } from 'react';
import Link from 'next/link';
import { apiClient, getApiErrorMessage } from '@/lib/api/client';
import { Alert } from '@/components/ui/Alert';
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

  return (
    <div className="max-w-6xl mx-auto px-6 py-12">
      <h1 className="text-3xl font-bold tracking-tight text-[#171717] mb-2">
        Explore Events
      </h1>
      <p className="text-[#4D4D4D] mb-8">Find your next experience</p>

      <form
        className="flex flex-col sm:flex-row gap-3 sm:items-center mb-8"
        onSubmit={(e) => {
          e.preventDefault();
          setQuery(search);
        }}
      >
        <label htmlFor="event-search" className="sr-only">
          Cari event
        </label>
        <input
          id="event-search"
          type="search"
          placeholder="Cari event"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          className="w-full sm:max-w-sm border border-[#E2E8F0] rounded-lg px-4 py-2.5 text-sm bg-white focus:outline-none focus:ring-2 focus:ring-[#D08080] focus:border-transparent transition-all"
        />
        <button
          type="submit"
          className="inline-flex h-10 items-center justify-center rounded-lg border border-[#E2E8F0] bg-white px-4 text-sm font-medium text-[#171717] transition-colors hover:bg-[#FAFAFA]"
        >
          Cari
        </button>
      </form>

      {error && (
        <Alert variant="error" className="mb-6">
          {error}
        </Alert>
      )}

      {loading ? (
        <p className="text-[#4D4D4D] py-10 text-center">Memuat event...</p>
      ) : events.length === 0 ? (
        <div className="bg-white border border-[#E2E8F0] rounded-xl p-6 text-center">
          <p className="text-[#4D4D4D]">Belum ada event yang dijual saat ini.</p>
        </div>
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
          {events.map((event) => (
            <Link
              key={event.event_id}
              href={`/customer/events/${event.event_id}`}
              className="group bg-white border border-[#E2E8F0] rounded-xl p-6 hover:border-[#D08080] hover:-translate-y-1 hover:shadow-md transition-all duration-200 flex flex-col"
            >
              <div className="flex-1">
                <h3 className="font-bold text-lg text-[#171717] mb-1">
                  {event.name}
                </h3>
                <p className="text-sm text-[#8F8F8F]">
                  {event.event_date} &bull; {event.venue_name}
                </p>
                <p className="text-sm text-[#4D4D4D] leading-relaxed mt-3 line-clamp-3">
                  {event.description}
                </p>
                <dl className="mt-4 space-y-1 text-sm">
                  <div className="flex justify-between">
                    <dt className="text-[#4D4D4D]">Mulai dari</dt>
                    <dd className="font-medium text-[#171717]">
                      {formatRupiah(event.min_price)}
                    </dd>
                  </div>
                  <div className="flex justify-between">
                    <dt className="text-[#4D4D4D]">Sisa tiket</dt>
                    <dd className="font-medium text-[#171717]">
                      {event.tickets_available} / {event.total_tickets}
                    </dd>
                  </div>
                </dl>
              </div>
              <span className="mt-6 inline-flex w-full items-center justify-center rounded-lg border border-[#D08080] bg-white px-4 py-2.5 text-sm font-medium text-[#D08080] transition-colors group-hover:bg-[#D08080] group-hover:text-[#171717] hover:bg-[#D08080] hover:text-[#171717]">
                Get Tickets
              </span>
            </Link>
          ))}
        </div>
      )}
    </div>
  );
}
