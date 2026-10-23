'use client';

import { useEffect, useState } from 'react';
import { useParams } from 'next/navigation';
import { apiClient, getApiErrorMessage } from '@/lib/api/client';
import { Alert } from '@/components/ui/Alert';
import { Button } from '@/components/ui/Button';
import { Card } from '@/components/ui/Card';
import { formatRupiah } from '@/lib/format';

interface SectionDetail {
  section_id: string;
  name: string;
  price: string;
  total_seats: number;
  available_seats: number;
}

interface EventDetail {
  event_id: string;
  name: string;
  description: string;
  venue_name: string;
  venue_address: string;
  event_date: string;
  event_start_time: string;
  event_end_time: string;
  status: string;
  organizer: { organizer_id: string; name: string };
  sections: SectionDetail[];
  seat_layout_version: number;
}

/**
 * Event Detail (HALAMAN 2.1 -> section picker).
 * Backed by GET /events/{event_id} (API CONTRACT §8.2).
 */
export default function EventDetailPage() {
  const params = useParams<{ id: string }>();
  const eventId = params?.id;

  const [event, setEvent] = useState<EventDetail | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  useEffect(() => {
    if (!eventId) return;
    let active = true;
    setLoading(true);
    setError('');
    apiClient
      .get<{ data: EventDetail }>(`/events/${eventId}`)
      .then((response) => {
        if (active) setEvent(response.data.data);
      })
      .catch((err) => {
        if (active) setError(getApiErrorMessage(err, 'Gagal memuat detail event'));
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [eventId]);

  return (
    <div className="min-h-screen bg-gray-50 text-gray-900">
      <header className="bg-white border-b border-gray-200">
        <div className="max-w-4xl mx-auto px-4 py-4 flex items-center justify-between">
          <Button href="/customer/dashboard" variant="outline">
            &larr; Katalog
          </Button>
          <Button href="/customer/my-tickets" variant="outline">
            Tiket Saya
          </Button>
        </div>
      </header>

      <main className="max-w-4xl mx-auto px-4 py-8">
        {loading && (
          <p className="text-gray-500 py-10 text-center">Memuat event...</p>
        )}

        {!loading && error && (
          <Alert variant="error" title="Event tidak dapat dimuat">
            {error}
          </Alert>
        )}

        {!loading && !error && event && (
          <>
            <h1 className="text-3xl font-bold text-gray-900 mb-2">
              {event.name}
            </h1>
            <p className="text-gray-600 mb-1">
              {event.venue_name} &bull; {event.venue_address}
            </p>
            <p className="text-gray-600 mb-6">
              {event.event_date} &bull; {event.event_start_time?.slice(0, 5)}
              {event.event_end_time
                ? ` - ${event.event_end_time.slice(0, 5)}`
                : ''}{' '}
              &bull; {event.organizer?.name}
            </p>

            <Card className="mb-8">
              <p className="text-gray-700 leading-relaxed">
                {event.description}
              </p>
            </Card>

            <h2 className="text-xl font-bold text-gray-900 mb-4">
              Pilih Section
            </h2>
            {event.sections?.length ? (
              <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                {event.sections.map((section) => {
                  const soldOut = section.available_seats <= 0;
                  return (
                    <Card key={section.section_id}>
                      <div className="flex items-start justify-between gap-4">
                        <div>
                          <h3 className="font-bold text-gray-900">
                            {section.name}
                          </h3>
                          <p className="text-sm text-gray-600">
                            {formatRupiah(section.price)}
                          </p>
                          <p className="text-xs text-gray-500 mt-1">
                            Sisa: {section.available_seats} /{' '}
                            {section.total_seats} kursi
                          </p>
                        </div>
                        <Button
                          href={`/customer/waiting-room/${event.event_id}?sectionId=${section.section_id}`}
                          variant={soldOut ? 'outline' : 'primary'}
                          disabled={soldOut}
                        >
                          {soldOut ? 'Habis' : 'Beli'}
                        </Button>
                      </div>
                    </Card>
                  );
                })}
              </div>
            ) : (
              <Alert variant="info">
                Event ini belum memiliki section yang bisa dibeli.
              </Alert>
            )}
          </>
        )}
      </main>
    </div>
  );
}
