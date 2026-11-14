'use client';

import { useState, type FormEvent } from 'react';
import { useRouter } from 'next/navigation';
import { apiClient, getApiErrorMessage } from '@/lib/api/client';
import { Alert } from '@/components/ui/Alert';

const inputClass =
  'w-full border border-[#E2E8F0] rounded-lg px-4 py-2.5 text-sm focus:outline-none focus:ring-2 focus:ring-[#D08080] focus:border-transparent transition-all';

export default function CreateEventPage() {
  const router = useRouter();
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [venueName, setVenueName] = useState('');
  const [venueAddress, setVenueAddress] = useState('');
  const [eventDate, setEventDate] = useState('');
  const [startTime, setStartTime] = useState('19:00');
  const [endTime, setEndTime] = useState('');
  const [seatingMode, setSeatingMode] = useState('ZONED');
  const [error, setError] = useState('');
  const [submitting, setSubmitting] = useState(false);

  const onSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setError('');
    setSubmitting(true);
    try {
      const response = await apiClient.post<{ data: { event_id: string } }>(
        '/eo/events',
        {
          name,
          description,
          venue_name: venueName,
          venue_address: venueAddress,
          event_date: eventDate,
          event_start_time: startTime,
          event_end_time: endTime,
          seating_mode: seatingMode,
        }
      );
      router.push(`/eo/events/${response.data.data.event_id}/layout`);
    } catch (err) {
      setError(getApiErrorMessage(err, 'Gagal membuat event.'));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="mx-auto max-w-xl">
      <h1 className="text-2xl font-bold tracking-tight">Create Event</h1>
      <p className="mt-1 text-sm text-[#4D4D4D]">
        Event dibuat sebagai DRAFT. Atur layout peta setelah ini.
      </p>

      {error && (
        <Alert variant="error" className="mt-6">
          {error}
        </Alert>
      )}

      <form onSubmit={onSubmit} className="mt-6 space-y-4" noValidate>
        <div>
          <label htmlFor="event-name" className="block text-sm font-medium mb-1.5">
            Nama Event
          </label>
          <input
            id="event-name"
            required
            value={name}
            onChange={(e) => setName(e.target.value)}
            className={inputClass}
          />
        </div>
        <div>
          <label
            htmlFor="event-description"
            className="block text-sm font-medium mb-1.5"
          >
            Deskripsi
          </label>
          <textarea
            id="event-description"
            rows={3}
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            className={inputClass}
          />
        </div>
        <div className="grid gap-4 sm:grid-cols-2">
          <div>
            <label htmlFor="venue-name" className="block text-sm font-medium mb-1.5">
              Venue
            </label>
            <input
              id="venue-name"
              required
              value={venueName}
              onChange={(e) => setVenueName(e.target.value)}
              className={inputClass}
            />
          </div>
          <div>
            <label htmlFor="venue-address" className="block text-sm font-medium mb-1.5">
              Alamat
            </label>
            <input
              id="venue-address"
              required
              value={venueAddress}
              onChange={(e) => setVenueAddress(e.target.value)}
              className={inputClass}
            />
          </div>
          <div>
            <label htmlFor="event-date" className="block text-sm font-medium mb-1.5">
              Tanggal
            </label>
            <input
              id="event-date"
              type="date"
              required
              value={eventDate}
              onChange={(e) => setEventDate(e.target.value)}
              className={inputClass}
            />
          </div>
          <div>
            <label htmlFor="event-mode" className="block text-sm font-medium mb-1.5">
              Seating Mode
            </label>
            <select
              id="event-mode"
              value={seatingMode}
              onChange={(e) => setSeatingMode(e.target.value)}
              className={inputClass}
            >
              <option value="ZONED">ZONED</option>
              <option value="ASSIGNED">ASSIGNED</option>
            </select>
          </div>
          <div>
            <label htmlFor="event-start" className="block text-sm font-medium mb-1.5">
              Jam Mulai
            </label>
            <input
              id="event-start"
              type="time"
              required
              value={startTime}
              onChange={(e) => setStartTime(e.target.value)}
              className={inputClass}
            />
          </div>
          <div>
            <label htmlFor="event-end" className="block text-sm font-medium mb-1.5">
              Jam Selesai (opsional)
            </label>
            <input
              id="event-end"
              type="time"
              value={endTime}
              onChange={(e) => setEndTime(e.target.value)}
              className={inputClass}
            />
          </div>
        </div>

        <div className="flex justify-end gap-2 pt-2">
          <button
            type="button"
            onClick={() => router.push('/eo/dashboard')}
            className="rounded-lg border border-[#E2E8F0] px-4 py-2.5 text-sm font-medium hover:bg-[#FAFAFA]"
          >
            Batal
          </button>
          <button
            type="submit"
            disabled={submitting}
            className="rounded-lg bg-[#D08080] px-4 py-2.5 text-sm font-medium text-[#171717] hover:bg-[#c06b6b] disabled:opacity-50"
          >
            {submitting ? 'Menyimpan...' : 'Buat Event'}
          </button>
        </div>
      </form>
    </div>
  );
}
