'use client';

import { useMemo, useState } from 'react';
import { useParams } from 'next/navigation';
import {
  apiClient,
  createIdempotencyKey,
  getApiErrorMessage,
} from '@/lib/api/client';
import { Alert } from '@/components/ui/Alert';
import { Button } from '@/components/ui/Button';
import { Card } from '@/components/ui/Card';
import { Input } from '@/components/ui/Input';

interface AssignmentRow {
  key: string;
  ticketId: string;
  userId: string;
}

interface BulkAssignTicketResult {
  ticket_id: string;
  assigned_to?: string;
  status: string;
  error?: string;
}

interface BulkAssignResponse {
  booking_id: string;
  assigned: number;
  tickets: BulkAssignTicketResult[];
}

const UUID_PATTERN =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

function newRow(index: number): AssignmentRow {
  return { key: `${Date.now()}-${index}`, ticketId: '', userId: '' };
}

export default function BulkAssignPage() {
  const params = useParams<{ bookingId: string }>();
  const bookingId = params?.bookingId ?? '';

  const [rows, setRows] = useState<AssignmentRow[]>([newRow(0)]);
  const [result, setResult] = useState<BulkAssignResponse | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  const invalidRows = useMemo(
    () =>
      rows.some(
        (row) =>
          !UUID_PATTERN.test(row.ticketId.trim()) ||
          !UUID_PATTERN.test(row.userId.trim())
      ),
    [rows]
  );

  const updateRow = (
    key: string,
    field: 'ticketId' | 'userId',
    value: string
  ) => {
    setRows((prev) =>
      prev.map((row) => (row.key === key ? { ...row, [field]: value } : row))
    );
  };

  const addRow = () => {
    setRows((prev) => [...prev, newRow(prev.length)]);
  };

  const removeRow = (key: string) => {
    setRows((prev) =>
      prev.length === 1 ? prev : prev.filter((row) => row.key !== key)
    );
  };

  const handleAssign = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setError('');
    setResult(null);

    if (!bookingId) {
      setError('Booking ID tidak valid.');
      return;
    }
    if (invalidRows) {
      setError('Semua Ticket ID dan User ID harus berupa UUID yang valid.');
      return;
    }

    const assignments = rows.map((row) => ({
      ticket_id: row.ticketId.trim(),
      user_id: row.userId.trim(),
    }));

    setLoading(true);
    try {
      const response = await apiClient.post<{ data: BulkAssignResponse }>(
        '/payments/bulk/assign',
        { booking_id: bookingId, assignments },
        { headers: { 'X-Idempotency-Key': createIdempotencyKey() } }
      );
      setResult(response.data.data);
    } catch (err) {
      setError(getApiErrorMessage(err, 'Gagal assign tiket'));
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="min-h-screen bg-gray-50 text-gray-900">
      <header className="bg-white border-b border-gray-200">
        <div className="max-w-2xl mx-auto px-4 py-4">
          <Button href="/customer/my-tickets" variant="outline">
            &larr; Tiket Saya
          </Button>
        </div>
      </header>

      <main className="max-w-2xl mx-auto px-4 py-8">
        <h1 className="text-3xl font-bold mb-2">Bulk Ticket Assignment</h1>
        <p className="text-sm text-gray-500 mb-6">
          Assign tiket ke anggota grup Anda. Masukkan Ticket ID dan User ID
          anggota untuk setiap tiket.
        </p>

        {error && (
          <Alert variant="error" className="mb-4">
            {error}
          </Alert>
        )}

        {result && (
          <Alert variant="success" className="mb-4">
            <p className="font-semibold">
              {result.assigned} tiket berhasil di-assign.
            </p>
            <ul className="mt-2 space-y-1 text-sm">
              {result.tickets.map((ticket) => (
                <li key={ticket.ticket_id}>
                  {ticket.ticket_id}: {ticket.status}
                  {ticket.assigned_to ? ` -> ${ticket.assigned_to}` : ''}
                  {ticket.error ? ` (${ticket.error})` : ''}
                </li>
              ))}
            </ul>
            <Button href="/customer/my-tickets" className="mt-3">
              Lihat Tiket Saya
            </Button>
          </Alert>
        )}

        <Card>
          <form onSubmit={handleAssign} className="space-y-5">
            {rows.map((row, index) => (
              <div
                key={row.key}
                className="border border-gray-200 rounded-lg p-4 space-y-3"
              >
                <div className="flex items-center justify-between gap-3">
                  <p className="font-semibold text-gray-900">
                    Tiket #{index + 1}
                  </p>
                  {rows.length > 1 && (
                    <Button
                      type="button"
                      variant="outline"
                      className="text-red-600 border-red-300 hover:bg-red-50"
                      onClick={() => removeRow(row.key)}
                    >
                      Hapus
                    </Button>
                  )}
                </div>
                <Input
                  label="Ticket ID (UUID)"
                  placeholder="00000000-0000-0000-0000-000000000000"
                  value={row.ticketId}
                  onChange={(event) =>
                    updateRow(row.key, 'ticketId', event.target.value)
                  }
                  autoComplete="off"
                  required
                />
                <Input
                  label="User ID Anggota (UUID)"
                  placeholder="00000000-0000-0000-0000-000000000000"
                  value={row.userId}
                  onChange={(event) =>
                    updateRow(row.key, 'userId', event.target.value)
                  }
                  autoComplete="off"
                  required
                />
              </div>
            ))}

            <Button
              type="button"
              variant="secondary"
              className="w-full"
              onClick={addRow}
              disabled={loading}
            >
              + Tambah Tiket
            </Button>

            <Button
              type="submit"
              className="w-full"
              isLoading={loading}
              disabled={invalidRows}
            >
              Assign Tiket
            </Button>
          </form>
        </Card>
      </main>
    </div>
  );
}
