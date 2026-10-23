'use client';

import { useCallback, useEffect, useState } from 'react';
import { apiClient, getApiErrorMessage } from '@/lib/api/client';
import { Alert } from '@/components/ui/Alert';
import { Button } from '@/components/ui/Button';
import { Card } from '@/components/ui/Card';
import { Input } from '@/components/ui/Input';
import { formatRupiah } from '@/lib/format';

const ADMIN_2FA_STORAGE_KEY = 'e-tix-flash:admin-2fa-token';

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

export default function AdminLedger() {
  const [entries, setEntries] = useState<LedgerEntry[]>([]);
  const [summary, setSummary] = useState<LedgerSummary | null>(null);
  const [twoFA, setTwoFA] = useState('');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  const fetchLedger = useCallback(async (token: string) => {
    setLoading(true);
    setError('');
    try {
      const response = await apiClient.get<{ data: LedgerData }>(
        '/admin/ledger',
        {
          headers: token ? { 'X-Admin-2FA-Token': token } : undefined,
        }
      );
      setEntries(response.data.data.entries ?? []);
      setSummary(response.data.data.summary);
    } catch (err: unknown) {
      setError(getApiErrorMessage(err, 'Gagal memuat ledger. Periksa 2FA.'));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    const stored = readAdmin2FAToken();
    setTwoFA(stored);
    void fetchLedger(stored);
  }, [fetchLedger]);

  const handleTokenChange = (token: string) => {
    setTwoFA(token);
    saveAdmin2FAToken(token);
  };

  return (
    <div className="p-6 md:p-10 max-w-6xl mx-auto">
      <h1 className="text-3xl font-bold mb-6">Ledger Audit</h1>

      <div className="flex flex-col sm:flex-row gap-4 mb-6 sm:items-end">
        <div className="sm:w-64">
          <Input
            label="2FA Token"
            value={twoFA}
            placeholder="Kode TOTP admin"
            onChange={(event) => handleTokenChange(event.target.value)}
          />
        </div>
        <Button onClick={() => void fetchLedger(twoFA)} isLoading={loading}>
          Muat Ulang
        </Button>
      </div>

      {error && (
        <Alert variant="error" className="mb-6">
          {error}
        </Alert>
      )}

      {summary && (
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4 mb-6">
          <Card>
            <h3 className="text-gray-500 text-sm">Total Paid</h3>
            <p className="text-2xl font-bold">{formatRupiah(summary.total_paid)}</p>
          </Card>
          <Card>
            <h3 className="text-gray-500 text-sm">Total Pending</h3>
            <p className="text-2xl font-bold">{formatRupiah(summary.total_pending)}</p>
          </Card>
          <Card>
            <h3 className="text-gray-500 text-sm">Total Refunded</h3>
            <p className="text-2xl font-bold">{formatRupiah(summary.total_refunded)}</p>
          </Card>
          <Card>
            <h3 className="text-gray-500 text-sm">Unreconciled</h3>
            <p className="text-2xl font-bold">{summary.unreconciled_count}</p>
          </Card>
        </div>
      )}

      <Card>
        {loading ? (
          <p className="text-center text-gray-500 py-6">Memuat ledger...</p>
        ) : entries.length === 0 ? (
          <p className="text-center text-gray-500 py-6">Belum ada entri ledger.</p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b text-left text-gray-500">
                  <th className="py-2 pr-4">Event Type</th>
                  <th className="py-2 pr-4">Reference</th>
                  <th className="py-2 pr-4">Amount</th>
                  <th className="py-2 pr-4">Status</th>
                  <th className="py-2 pr-4">Reconciled</th>
                  <th className="py-2">Date</th>
                </tr>
              </thead>
              <tbody>
                {entries.map((entry, index) => (
                  <tr
                    key={`${entry.reference_type}-${entry.reference_id}-${index}`}
                    className="border-b"
                  >
                    <td className="py-2 pr-4 font-medium">{entry.event_type}</td>
                    <td className="py-2 pr-4 text-gray-500">
                      {entry.reference_type}
                    </td>
                    <td className="py-2 pr-4">{formatRupiah(entry.amount)}</td>
                    <td className="py-2 pr-4">
                      <span className="px-2 py-1 bg-gray-100 rounded text-xs">
                        {entry.status}
                      </span>
                    </td>
                    <td className="py-2 pr-4">{entry.reconciled ? 'Yes' : 'No'}</td>
                    <td className="py-2">{formatDateTime(entry.created_at)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
    </div>
  );
}
