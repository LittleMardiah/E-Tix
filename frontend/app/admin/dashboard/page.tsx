'use client';

import { useEffect, useState } from 'react';
import { apiClient, getApiErrorMessage } from '@/lib/api/client';
import { Alert } from '@/components/ui/Alert';

const PAGE_SIZE = 10;

interface LedgerEntry {
  event_id: string;
  event_type: string;
  reference_type: string;
  reference_id: string;
  booking_id?: string;
  admin_id?: string;
  amount: string;
  status: string;
  created_at: string;
}

interface Kpi {
  totalEvents: number | null;
  totalUsers: number | null;
}

function formatDateTime(value: string): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime())
    ? value
    : date.toLocaleString('id-ID', {
        dateStyle: 'medium',
        timeStyle: 'short',
      });
}

function shortId(value?: string): string {
  return value ? value.slice(0, 8) : 'SYSTEM';
}

export default function AdminDashboardPage() {
  const [kpi, setKpi] = useState<Kpi>({ totalEvents: null, totalUsers: null });
  const [entries, setEntries] = useState<LedgerEntry[]>([]);
  const [page, setPage] = useState(1);
  const [hasNextPage, setHasNextPage] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  useEffect(() => {
    let active = true;
    const loadKpi = async () => {
      const [eventsResult, usersResult] = await Promise.allSettled([
        apiClient.get<{ meta?: { total?: number } }>('/events', {
          params: { limit: 1 },
        }),
        apiClient.get<{
          data: { users: unknown[] };
        }>('/admin/users', { params: { limit: 100 } }),
      ]);
      if (!active) return;
      setKpi({
        totalEvents:
          eventsResult.status === 'fulfilled'
            ? eventsResult.value.data.meta?.total ?? 0
            : null,
        totalUsers:
          usersResult.status === 'fulfilled'
            ? usersResult.value.data.data.users.length
            : null,
      });
    };
    void loadKpi();
    return () => {
      active = false;
    };
  }, []);

  useEffect(() => {
    let active = true;
    const load = async () => {
      setLoading(true);
      setError('');
      try {
        const response = await apiClient.get<{
          data: { entries: LedgerEntry[] };
        }>('/admin/ledger', { params: { page, limit: PAGE_SIZE } });
        if (!active) return;
        const nextEntries = response.data.data.entries ?? [];
        setEntries(nextEntries);
        setHasNextPage(nextEntries.length === PAGE_SIZE);
      } catch (err) {
        if (active) setError(getApiErrorMessage(err, 'Gagal memuat audit log.'));
      } finally {
        if (active) setLoading(false);
      }
    };
    void load();
    return () => {
      active = false;
    };
  }, [page]);

  const cards = [
    {
      label: 'Total Event',
      value: kpi.totalEvents === null ? '—' : String(kpi.totalEvents),
    },
    {
      label: 'Total User',
      value: kpi.totalUsers === null ? '—' : String(kpi.totalUsers),
    },
  ];

  return (
    <div className="mx-auto max-w-5xl">
      <h1 className="text-2xl font-bold tracking-tight">Dashboard Admin</h1>
      <p className="mt-1 text-sm text-[#4D4D4D]">
        Ringkasan platform dan jejak aksi admin.
      </p>

      <div className="mt-6 grid gap-4 sm:grid-cols-2">
        {cards.map((card) => (
          <div
            key={card.label}
            className="rounded-2xl border border-[#E2E8F0] bg-white p-5 shadow-sm"
          >
            <p className="text-xs uppercase tracking-widest text-[#8F8F8F]">
              {card.label}
            </p>
            <p className="mt-2 text-3xl font-bold font-mono">{card.value}</p>
          </div>
        ))}
      </div>

      <section id="audit-log" className="mt-8">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <h2 className="text-lg font-semibold">Audit Log</h2>
          <div className="flex items-center gap-2 text-sm">
            <button
              type="button"
              onClick={() => setPage((current) => Math.max(1, current - 1))}
              disabled={page === 1 || loading}
              className="rounded-lg border border-[#E2E8F0] px-3 py-1.5 disabled:opacity-40"
            >
              Sebelumnya
            </button>
            <span className="text-[#4D4D4D]">Halaman {page}</span>
            <button
              type="button"
              onClick={() => setPage((current) => current + 1)}
              disabled={!hasNextPage || loading}
              className="rounded-lg border border-[#E2E8F0] px-3 py-1.5 disabled:opacity-40"
            >
              Berikutnya
            </button>
          </div>
        </div>

        {error && (
          <Alert variant="error" className="mt-4">
            {error}
          </Alert>
        )}

        <div className="mt-4 overflow-x-auto rounded-2xl border border-[#E2E8F0] bg-white">
          <table className="min-w-full text-sm">
            <thead className="bg-[#FAFAFA] text-left text-xs uppercase tracking-wider text-[#8F8F8F]">
              <tr>
                <th className="px-4 py-3">Waktu</th>
                <th className="px-4 py-3">Admin</th>
                <th className="px-4 py-3">Aksi</th>
                <th className="px-4 py-3">Resource</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-[#E2E8F0]">
              {loading ? (
                <tr>
                  <td colSpan={4} className="px-4 py-6 text-[#4D4D4D]">
                    Memuat audit log...
                  </td>
                </tr>
              ) : entries.length === 0 ? (
                <tr>
                  <td colSpan={4} className="px-4 py-6 text-[#4D4D4D]">
                    Belum ada aktivitas tercatat.
                  </td>
                </tr>
              ) : (
                entries.map((entry) => (
                  <tr key={`${entry.event_id}-${entry.reference_id}`}>
                    <td className="px-4 py-3 whitespace-nowrap">
                      {formatDateTime(entry.created_at)}
                    </td>
                    <td className="px-4 py-3 font-mono text-xs">
                      {shortId(entry.admin_id)}
                    </td>
                    <td className="px-4 py-3">{entry.event_type}</td>
                    <td className="px-4 py-3 font-mono text-xs">
                      {entry.reference_type}:{shortId(entry.reference_id)}
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </section>
    </div>
  );
}
