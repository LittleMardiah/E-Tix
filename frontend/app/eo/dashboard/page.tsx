'use client';

import { useEffect, useState } from 'react';
import Link from 'next/link';
import { apiClient, getApiErrorMessage } from '@/lib/api/client';
import { Alert } from '@/components/ui/Alert';
import { formatRupiah } from '@/lib/format';

interface EventSection {
  section_id: string;
  name: string;
  price: string;
}

interface EventSummary {
  event_id: string;
  name: string;
  description: string;
  venue_name: string;
  event_date: string;
  event_start_time: string;
  status: string;
  sections?: EventSection[];
}

function formatDate(date: string, time: string): string {
  const parsed = new Date(`${date}T${time || '00:00:00'}`);
  if (Number.isNaN(parsed.getTime())) return `${date} ${time}`.trim();
  return parsed.toLocaleString('id-ID', {
    dateStyle: 'medium',
    timeStyle: 'short',
  });
}

export default function EODashboardPage() {
  const [events, setEvents] = useState<EventSummary[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  useEffect(() => {
    let active = true;
    const load = async () => {
      try {
        const me = await apiClient.get<{ data: { user_id: string } }>('/users/me');
        const response = await apiClient.get<{
          data: { events: EventSummary[] };
        }>('/events', {
          params: { limit: 50, organizer_id: me.data.data.user_id },
        });
        if (active) setEvents(response.data.data.events ?? []);
      } catch (err) {
        if (active) setError(getApiErrorMessage(err, 'Gagal memuat event.'));
      } finally {
        if (active) setLoading(false);
      }
    };
    void load();
    return () => {
      active = false;
    };
  }, []);

  return (
    <div className="mx-auto max-w-5xl">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Dashboard EO</h1>
          <p className="mt-1 text-sm text-[#4D4D4D]">
            Kelola event dan layout peta Anda.
          </p>
        </div>
        <Link
          href="/eo/events/new"
          className="rounded-lg bg-[#D08080] px-4 py-2.5 text-sm font-medium text-[#171717] transition-colors hover:bg-[#c06b6b]"
        >
          Create Event
        </Link>
      </div>

      {error && (
        <Alert variant="error" className="mt-6">
          {error}
        </Alert>
      )}

      <section id="events" className="mt-8">
        <h2 className="text-lg font-semibold">Event Saya</h2>
        {loading ? (
          <p className="mt-3 text-sm text-[#4D4D4D]">Memuat event...</p>
        ) : events.length === 0 ? (
          <div className="mt-3 rounded-2xl border border-dashed border-[#E2E8F0] bg-white p-8 text-center">
            <p className="text-sm text-[#4D4D4D]">
              Belum ada event. Buat event pertama Anda.
            </p>
            <Link
              href="/eo/events/new"
              className="mt-3 inline-block text-sm font-medium text-[#D08080] hover:underline"
            >
              Create Event
            </Link>
          </div>
        ) : (
          <ul className="mt-3 grid gap-4 sm:grid-cols-2">
            {events.map((event) => (
              <li
                key={event.event_id}
                className="rounded-2xl border border-[#E2E8F0] bg-white p-5 shadow-sm"
              >
                <div className="flex items-start justify-between gap-3">
                  <h3 className="font-semibold leading-snug">{event.name}</h3>
                  <span className="shrink-0 rounded-full bg-[#FAF2F2] px-2 py-0.5 text-xs font-medium text-[#171717]">
                    {event.status}
                  </span>
                </div>
                <p className="mt-1 text-sm text-[#4D4D4D]">
                  {event.venue_name} &bull;{' '}
                  {formatDate(event.event_date, event.event_start_time)}
                </p>
                <p className="mt-1 text-xs text-[#8F8F8F]">
                  {(event.sections ?? []).length} seksi
                  {event.sections && event.sections.length > 0
                    ? ` \u2022 dari ${formatRupiah(event.sections[0].price)}`
                    : ''}
                </p>
                <div className="mt-4 flex flex-wrap gap-2">
                  <Link
                    href={`/eo/events/${event.event_id}/layout`}
                    className="rounded-lg border border-[#E2E8F0] px-3 py-2 text-sm font-medium transition-colors hover:bg-[#FAFAFA]"
                  >
                    Edit Map
                  </Link>
                  <Link
                    href={`/eo/dashboard/${event.event_id}`}
                    className="rounded-lg border border-[#E2E8F0] px-3 py-2 text-sm font-medium transition-colors hover:bg-[#FAFAFA]"
                  >
                    Statistik
                  </Link>
                  <Link
                    href={`/events/${event.event_id}`}
                    className="rounded-lg px-3 py-2 text-sm font-medium text-[#D08080] hover:underline"
                  >
                    Halaman Publik
                  </Link>
                </div>
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}
