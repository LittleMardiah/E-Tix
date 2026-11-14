'use client';

import { useCallback, useEffect, useState } from 'react';
import { apiClient, getApiErrorMessage } from '@/lib/api/client';
import { Alert } from '@/components/ui/Alert';
import { formatRupiah } from '@/lib/format';

const ADMIN_2FA_STORAGE_KEY = 'e-tix-flash:admin-2fa-token';
// Matches the backend default page size ceiling (adminParsePagination: 1..100).
const PAGE_SIZE = 20;

function readAdmin2FAToken(): string {
  if (typeof window === 'undefined') return '';
  return window.sessionStorage.getItem(ADMIN_2FA_STORAGE_KEY) ?? '';
}

function saveAdmin2FAToken(token: string): void {
  if (typeof window === 'undefined') return;
  if (token) {
    window.sessionStorage.setItem(ADMIN_2FA_STORAGE_KEY, token);
  } else {
    window.sessionStorage.removeItem(ADMIN_2FA_STORAGE_KEY);
  }
}

interface LedgerEntry {
  event_id: string;
  event_type: string;
  reference_type: string;
  reference_id: string;
  booking_id?: string;
  user_id?: string;
  amount: string;
  status: string;
  gateway_transaction_id?: string;
  admin_id?: string;
  reconciled: boolean;
  created_at: string;
}

interface LedgerSummary {
  total_paid: string;
  total_pending: string;
  total_refunded: string;
  unreconciled_count: number;
}

interface LedgerData {
  entries: LedgerEntry[];
  summary: LedgerSummary;
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

function statusBadgeClass(status: string): string {
  const key = status.toUpperCase();
  if (key === 'PAID' || key === 'SUCCESS' || key === 'SETTLED' || key === 'CAPTURED') {
    return 'bg-green-50 text-green-700 border-green-100';
  }
  if (key === 'PENDING' || key === 'UNRECONCILED') {
    return 'bg-amber-50 text-amber-700 border-amber-100';
  }
  if (key === 'REFUNDED' || key === 'PARTIALLY_REFUNDED') {
    return 'bg-sky-50 text-sky-700 border-sky-100';
  }
  if (key === 'FAILED' || key === 'EXPIRED' || key === 'CANCELLED') {
    return 'bg-rose-50 text-rose-700 border-rose-100';
  }
  return 'bg-[#FAFAFA] text-[#4D4D4D] border-[#E2E8F0]';
}

export default function AdminLedger() {
  const [entries, setEntries] = useState<LedgerEntry[]>([]);
  const [summary, setSummary] = useState<LedgerSummary | null>(null);
  const [twoFA, setTwoFA] = useState('');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [page, setPage] = useState(1);

  const fetchLedger = useCallback(async (token: string, pageNumber: number) => {
    setLoading(true);
    setError('');
    try {
      const response = await apiClient.get<{ data: LedgerData }>(
        '/admin/ledger',
        {
          headers: token ? { 'X-Admin-2FA-Token': token } : undefined,
          params: { page: pageNumber, limit: PAGE_SIZE },
        }
      );
      setEntries(response.data.data.entries ?? []);
      setSummary(response.data.data.summary);
      setPage(pageNumber);
    } catch (err: unknown) {
      setError(getApiErrorMessage(err, 'Gagal memuat ledger. Periksa 2FA.'));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    const stored = readAdmin2FAToken();
    setTwoFA(stored);
    void fetchLedger(stored, 1);
  }, [fetchLedger]);

  // The response has no total count, so "more pages" is inferred from a full
  // page. That is exact for every page except a boundary page that happens to
  // be exactly full; Next then lands on an empty page and disables itself.
  const hasNextPage = entries.length === PAGE_SIZE;

  const handleTokenChange = (token: string) => {
    setTwoFA(token);
    saveAdmin2FAToken(token);
  };

  return (
    <div className="max-w-6xl mx-auto px-6 py-12">
      <h1 className="text-3xl font-bold tracking-tight text-[#171717] mb-6">
        Ledger Audit
      </h1>

      <div className="flex flex-col sm:flex-row gap-4 mb-6 sm:items-end">
        <div className="sm:w-64">
          <label
            htmlFor="admin-2fa"
            className="block text-sm font-medium text-[#171717] mb-1.5"
          >
            2FA Token
          </label>
          <input
            id="admin-2fa"
            value={twoFA}
            placeholder="Kode TOTP admin"
            onChange={(event) => handleTokenChange(event.target.value)}
            className="w-full border border-[#E2E8F0] rounded-lg px-4 py-2.5 text-sm focus:outline-none focus:ring-2 focus:ring-[#D08080] focus:border-transparent transition-all"
          />
        </div>
        <button
          type="button"
          onClick={() => void fetchLedger(twoFA, page)}
          disabled={loading}
          className="bg-[#D08080] text-[#171717] font-medium rounded-lg px-5 py-2.5 hover:bg-[#c06b6b] transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {loading ? 'Memuat...' : 'Muat Ulang'}
        </button>
      </div>

      {error && (
        <Alert variant="error" className="mb-6">
          {error}
        </Alert>
      )}

      {summary && (
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4 mb-6">
          <div className="bg-white border border-[#E2E8F0] rounded-xl p-6">
            <h3 className="text-xs font-medium uppercase tracking-wide text-[#4D4D4D]">Total Paid</h3>
            <p className="text-2xl font-mono text-[#D08080] mt-2">{formatRupiah(summary.total_paid)}</p>
          </div>
          <div className="bg-white border border-[#E2E8F0] rounded-xl p-6">
            <h3 className="text-xs font-medium uppercase tracking-wide text-[#4D4D4D]">Total Pending</h3>
            <p className="text-2xl font-mono text-[#D08080] mt-2">{formatRupiah(summary.total_pending)}</p>
          </div>
          <div className="bg-white border border-[#E2E8F0] rounded-xl p-6">
            <h3 className="text-xs font-medium uppercase tracking-wide text-[#4D4D4D]">Total Refunded</h3>
            <p className="text-2xl font-mono text-[#D08080] mt-2">{formatRupiah(summary.total_refunded)}</p>
          </div>
          <div className="bg-white border border-[#E2E8F0] rounded-xl p-6">
            <h3 className="text-xs font-medium uppercase tracking-wide text-[#4D4D4D]">Unreconciled</h3>
            <p className="text-2xl font-mono text-[#D08080] mt-2">{summary.unreconciled_count}</p>
          </div>
        </div>
      )}

      <div className="bg-white border border-[#E2E8F0] rounded-xl overflow-hidden">
        {loading ? (
          <p className="text-center text-[#4D4D4D] py-10 text-sm">Memuat ledger...</p>
        ) : entries.length === 0 ? (
          <p className="text-center text-[#4D4D4D] py-10 text-sm">Belum ada entri ledger.</p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm border-collapse">
              <thead>
                <tr className="bg-[#FAFAFA] text-left text-xs uppercase tracking-wide text-[#4D4D4D]">
                  <th className="px-4 py-3 border-b border-[#E2E8F0] font-medium">Event Type</th>
                  <th className="px-4 py-3 border-b border-[#E2E8F0] font-medium">Reference</th>
                  <th className="px-4 py-3 border-b border-[#E2E8F0] font-medium">Amount</th>
                  <th className="px-4 py-3 border-b border-[#E2E8F0] font-medium">Status</th>
                  <th className="px-4 py-3 border-b border-[#E2E8F0] font-medium">Reconciled</th>
                  <th className="px-4 py-3 border-b border-[#E2E8F0] font-medium">Date</th>
                </tr>
              </thead>
              <tbody>
                {entries.map((entry, index) => (
                  <tr
                    key={`${entry.reference_type}-${entry.reference_id}-${index}`}
                    className="border-b border-[#E2E8F0] last:border-0 hover:bg-[#FAFAFA] transition-colors"
                  >
                    <td className="px-4 py-3 font-medium text-[#171717]">{entry.event_type}</td>
                    <td className="px-4 py-3 text-[#4D4D4D]">
                      {entry.reference_type}
                    </td>
                    <td className="px-4 py-3 font-mono text-[#171717]">
                      {formatRupiah(entry.amount)}
                    </td>
                    <td className="px-4 py-3">
                      <span
                        className={`inline-block px-2.5 py-1 rounded-full border text-xs font-medium ${statusBadgeClass(entry.status)}`}
                      >
                        {entry.status}
                      </span>
                    </td>
                    <td className="px-4 py-3">
                      <span
                        className={`inline-block px-2.5 py-1 rounded-full border text-xs font-medium ${
                          entry.reconciled
                            ? 'bg-green-50 text-green-700 border-green-100'
                            : 'bg-amber-50 text-amber-700 border-amber-100'
                        }`}
                      >
                        {entry.reconciled ? 'Yes' : 'No'}
                      </span>
                    </td>
                    <td className="px-4 py-3 text-[#4D4D4D]">
                      {formatDateTime(entry.created_at)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}

        <div className="flex flex-wrap items-center justify-between gap-3 border-t border-[#E2E8F0] px-4 py-3">
          <p className="text-xs text-[#4D4D4D]">Halaman {page}</p>
          <div className="flex gap-2">
            <button
              type="button"
              onClick={() => void fetchLedger(twoFA, page - 1)}
              disabled={loading || page <= 1}
              className="rounded-lg border border-[#E2E8F0] px-4 py-2 text-sm font-medium text-[#171717] transition-colors hover:border-[#D08080] disabled:cursor-not-allowed disabled:opacity-50"
            >
              Sebelumnya
            </button>
            <button
              type="button"
              onClick={() => void fetchLedger(twoFA, page + 1)}
              disabled={loading || !hasNextPage}
              className="rounded-lg border border-[#E2E8F0] px-4 py-2 text-sm font-medium text-[#171717] transition-colors hover:border-[#D08080] disabled:cursor-not-allowed disabled:opacity-50"
            >
              Berikutnya
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
