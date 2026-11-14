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
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [submitting, setSubmitting] = useState(false);

  const clearFieldError = (field: string) => {
    setFieldErrors((current) => {
      if (!current[field]) return current;
      const next = { ...current };
      delete next[field];
      return next;
    });
  };

  // Red ring is used instead of swapping border colours so the design-system
  // border (#E2E8F0) stays intact and the error is still obvious.
  const fieldClass = (field: string) =>
    `${inputClass} ${fieldErrors[field] ? 'ring-1 ring-red-500' : ''}`;

  const onSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setError('');

    // Mirrors EventService.Create (backend/internal/event/service.go:154):
    // required fields, no past date, end time after start time.
    const errors: Record<string, string> = {};
    if (!name.trim()) errors.name = 'Nama event wajib diisi.';
    if (!venueName.trim()) errors.venueName = 'Venue wajib diisi.';
    if (!venueAddress.trim()) errors.venueAddress = 'Alamat wajib diisi.';
    if (!eventDate) {
      errors.eventDate = 'Tanggal wajib diisi.';
    } else {
      const chosen = new Date(`${eventDate}T00:00:00`);
      const today = new Date();
      today.setHours(0, 0, 0, 0);
      if (Number.isNaN(chosen.getTime())) {
        errors.eventDate = 'Format tanggal tidak valid.';
      } else if (chosen < today) {
        errors.eventDate = 'Tanggal tidak boleh di masa lalu.';
      }
    }
    if (!startTime) errors.startTime = 'Jam mulai wajib diisi.';
    if (endTime && startTime && endTime <= startTime) {
      errors.endTime = 'Jam selesai harus setelah jam mulai.';
    }

    setFieldErrors(errors);
    if (Object.keys(errors).length > 0) return;

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
            aria-invalid={fieldErrors.name ? true : undefined}
            aria-describedby={fieldErrors.name ? 'event-name-error' : undefined}
            onChange={(e) => {
              setName(e.target.value);
              clearFieldError('name');
            }}
            className={fieldClass('name')}
          />
          {fieldErrors.name && (
            <p id="event-name-error" role="alert" className="mt-1 text-xs text-red-600">
              {fieldErrors.name}
            </p>
          )}
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
              aria-invalid={fieldErrors.venueName ? true : undefined}
              aria-describedby={fieldErrors.venueName ? 'venue-name-error' : undefined}
              onChange={(e) => {
                setVenueName(e.target.value);
                clearFieldError('venueName');
              }}
              className={fieldClass('venueName')}
            />
            {fieldErrors.venueName && (
              <p id="venue-name-error" role="alert" className="mt-1 text-xs text-red-600">
                {fieldErrors.venueName}
              </p>
            )}
          </div>
          <div>
            <label htmlFor="venue-address" className="block text-sm font-medium mb-1.5">
              Alamat
            </label>
            <input
              id="venue-address"
              required
              value={venueAddress}
              aria-invalid={fieldErrors.venueAddress ? true : undefined}
              aria-describedby={fieldErrors.venueAddress ? 'venue-address-error' : undefined}
              onChange={(e) => {
                setVenueAddress(e.target.value);
                clearFieldError('venueAddress');
              }}
              className={fieldClass('venueAddress')}
            />
            {fieldErrors.venueAddress && (
              <p id="venue-address-error" role="alert" className="mt-1 text-xs text-red-600">
                {fieldErrors.venueAddress}
              </p>
            )}
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
              aria-invalid={fieldErrors.eventDate ? true : undefined}
              aria-describedby={fieldErrors.eventDate ? 'event-date-error' : undefined}
              onChange={(e) => {
                setEventDate(e.target.value);
                clearFieldError('eventDate');
              }}
              className={fieldClass('eventDate')}
            />
            {fieldErrors.eventDate && (
              <p id="event-date-error" role="alert" className="mt-1 text-xs text-red-600">
                {fieldErrors.eventDate}
              </p>
            )}
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
              aria-invalid={fieldErrors.startTime ? true : undefined}
              aria-describedby={fieldErrors.startTime ? 'event-start-error' : undefined}
              onChange={(e) => {
                setStartTime(e.target.value);
                clearFieldError('startTime');
                clearFieldError('endTime');
              }}
              className={fieldClass('startTime')}
            />
            {fieldErrors.startTime && (
              <p id="event-start-error" role="alert" className="mt-1 text-xs text-red-600">
                {fieldErrors.startTime}
              </p>
            )}
          </div>
          <div>
            <label htmlFor="event-end" className="block text-sm font-medium mb-1.5">
              Jam Selesai (opsional)
            </label>
            <input
              id="event-end"
              type="time"
              value={endTime}
              aria-invalid={fieldErrors.endTime ? true : undefined}
              aria-describedby={fieldErrors.endTime ? 'event-end-error' : undefined}
              onChange={(e) => {
                setEndTime(e.target.value);
                clearFieldError('endTime');
              }}
              className={fieldClass('endTime')}
            />
            {fieldErrors.endTime && (
              <p id="event-end-error" role="alert" className="mt-1 text-xs text-red-600">
                {fieldErrors.endTime}
              </p>
            )}
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
