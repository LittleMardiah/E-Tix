import type { Metadata } from 'next';
import Link from 'next/link';
import { notFound } from 'next/navigation';
import { PublicBuyButton } from '@/components/PublicBuyButton';
import { formatRupiah } from '@/lib/format';

const API_BASE_URL =
  process.env.NEXT_PUBLIC_API_URL || 'http://localhost:8080';

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
  organizer?: { organizer_id: string; name: string };
  sections: SectionDetail[];
}

async function fetchEvent(id: string): Promise<EventDetail | null> {
  try {
    const response = await fetch(
      `${API_BASE_URL}/events/${encodeURIComponent(id)}`,
      { cache: 'no-store' }
    );
    if (!response.ok) return null;
    const body = (await response.json()) as { data?: EventDetail };
    return body.data ?? null;
  } catch {
    return null;
  }
}

export async function generateMetadata({
  params,
}: {
  params: Promise<{ id: string }>;
}): Promise<Metadata> {
  const { id } = await params;
  const event = await fetchEvent(id);
  if (!event) {
    return { title: 'Event tidak ditemukan — E-Tix Flash' };
  }
  return {
    title: `${event.name} — E-Tix Flash`,
    description:
      event.description ||
      `${event.name} di ${event.venue_name}, ${event.event_date}.`,
    openGraph: {
      title: event.name,
      description: event.description,
      type: 'website',
    },
  };
}

function formatDate(date: string, time: string): string {
  const parsed = new Date(`${date}T${time || '00:00:00'}`);
  if (Number.isNaN(parsed.getTime())) return `${date} ${time}`.trim();
  return parsed.toLocaleString('id-ID', {
    dateStyle: 'full',
    timeStyle: 'short',
  });
}

export default async function PublicEventPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = await params;
  const event = await fetchEvent(id);
  if (!event) notFound();

  const totalAvailable = (event.sections ?? []).reduce(
    (sum, section) => sum + section.available_seats,
    0
  );
  const soldOut = totalAvailable <= 0;

  return (
    <div className="min-h-screen bg-[#FAFAFA] text-[#171717]">
      <header className="border-b border-[#E2E8F0] bg-white">
        <div className="mx-auto flex max-w-4xl items-center justify-between px-6 py-4">
          <Link href="/" className="text-lg font-bold tracking-tight">
            E-Tix Flash
          </Link>
          <Link
            href="/auth/login"
            className="text-sm font-medium text-[#4D4D4D] hover:text-[#171717]"
          >
            Masuk
          </Link>
        </div>
      </header>

      <main className="mx-auto max-w-4xl px-6 py-10">
        <p className="text-xs uppercase tracking-widest text-[#8F8F8F]">
          {event.status}
        </p>
        <h1 className="mt-2 text-3xl font-bold tracking-tight sm:text-4xl">
          {event.name}
        </h1>
        <p className="mt-3 text-sm text-[#4D4D4D]">
          {event.venue_name} &bull; {event.venue_address}
        </p>
        <p className="mt-1 text-sm text-[#4D4D4D]">
          {formatDate(event.event_date, event.event_start_time)}
        </p>
        {event.description && (
          <p className="mt-6 whitespace-pre-line text-sm leading-relaxed text-[#4D4D4D]">
            {event.description}
          </p>
        )}

        <section className="mt-8">
          <h2 className="text-lg font-semibold">Tiket &amp; Harga</h2>
          <ul className="mt-3 divide-y divide-[#E2E8F0] rounded-2xl border border-[#E2E8F0] bg-white">
            {(event.sections ?? []).length === 0 ? (
              <li className="px-5 py-4 text-sm text-[#4D4D4D]">
                Tiket belum tersedia untuk event ini.
              </li>
            ) : (
              (event.sections ?? []).map((section) => (
                <li
                  key={section.section_id}
                  className="flex flex-wrap items-center justify-between gap-3 px-5 py-4"
                >
                  <div>
                    <p className="font-medium">{section.name}</p>
                    <p className="text-xs text-[#8F8F8F]">
                      {section.available_seats} dari {section.total_seats} tersisa
                    </p>
                  </div>
                  <p className="font-mono text-sm font-bold">
                    {formatRupiah(section.price)}
                  </p>
                </li>
              ))
            )}
          </ul>
        </section>

        <div className="mt-8 flex flex-col gap-3 sm:flex-row sm:items-center">
          {soldOut ? (
            <span className="w-full sm:w-auto rounded-lg bg-gray-200 px-6 py-3 text-center text-sm font-semibold text-gray-500">
              Sold Out
            </span>
          ) : (
            <PublicBuyButton eventId={event.event_id} />
          )}
          <p className="text-xs text-[#8F8F8F]">
            Anda akan diminta masuk sebelum memilih kursi/zona.
          </p>
        </div>
      </main>
    </div>
  );
}
