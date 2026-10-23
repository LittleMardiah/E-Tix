'use client';

import { useEffect, useState } from 'react';
import { useParams } from 'next/navigation';
import { apiClient, getApiErrorMessage } from '@/lib/api/client';
import { Alert } from '@/components/ui/Alert';
import { Button } from '@/components/ui/Button';
import { Card } from '@/components/ui/Card';
import { formatRupiah } from '@/lib/format';

interface SalesBlock {
  tickets_sold: number;
  total_tickets: number;
  sales_percentage: number;
  revenue_today: string;
  total_revenue: string;
}

interface GateStatusBlock {
  total_gates_active: number;
  gates?: Record<string, string>;
  total_scans_today: number;
}

interface AntiBotBlock {
  requests_blocked_today: number;
  suspicious_ips_flagged: number;
  captcha_triggers_today: number;
}

interface ActivityItem {
  timestamp: string;
  type: string;
  description: string;
}

interface DashboardAlert {
  level: string;
  message: string;
}

interface DashboardData {
  event_id: string;
  event_name: string;
  event_status: string;
  sales: SalesBlock;
  gate_status: GateStatusBlock;
  anti_bot: AntiBotBlock;
  recent_activity: ActivityItem[];
  alerts: DashboardAlert[];
  updated_at: string;
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

export default function EODashboard() {
  const params = useParams<{ eventId: string }>();
  const eventId = params?.eventId ?? '';

  const [data, setData] = useState<DashboardData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  useEffect(() => {
    if (!eventId) return;
    let cancelled = false;

    setLoading(true);
    setError('');
    apiClient
      .get<{ data: DashboardData }>(`/events/${eventId}/dashboard`)
      .then((response) => {
        if (!cancelled) setData(response.data.data);
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setError(getApiErrorMessage(err, 'Gagal memuat dashboard event'));
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });

    return () => {
      cancelled = true;
    };
  }, [eventId]);

  if (!eventId) {
    return (
      <div className="p-10 max-w-6xl mx-auto">
        <Alert variant="error">Event ID tidak valid.</Alert>
      </div>
    );
  }

  if (loading) {
    return <div className="p-10 text-center text-gray-500">Memuat dashboard...</div>;
  }

  if (error || !data) {
    return (
      <div className="p-10 max-w-6xl mx-auto">
        <Alert variant="error" title="Gagal memuat dashboard">
          {error || 'Data dashboard tidak tersedia.'}
        </Alert>
      </div>
    );
  }

  return (
    <div className="p-6 md:p-10 max-w-6xl mx-auto">
      <div className="flex flex-col gap-4 md:flex-row md:items-start md:justify-between mb-6">
        <div>
          <h1 className="text-3xl font-bold">EO Dashboard: {data.event_name}</h1>
          <p className="text-sm text-gray-500 mt-1">
            Status: {data.event_status} &bull; Diperbarui {formatDateTime(data.updated_at)}
          </p>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button href={`/eo/analytics/${data.event_id}`} variant="outline">
            Analytics
          </Button>
          <Button href={`/eo/post-event/${data.event_id}`} variant="outline">
            Post-Event Report
          </Button>
        </div>
      </div>

      {data.alerts?.length > 0 && (
        <div className="space-y-2 mb-6">
          {data.alerts.map((alert, index) => (
            <Alert
              key={`${alert.level}-${index}`}
              variant={alert.level === 'WARNING' ? 'warning' : 'info'}
            >
              {alert.message}
            </Alert>
          ))}
        </div>
      )}

      <div className="grid grid-cols-1 md:grid-cols-3 gap-4 mb-6">
        <Card>
          <h3 className="text-gray-500 text-sm">Tiket Terjual</h3>
          <p className="text-3xl font-bold">{data.sales.tickets_sold}</p>
          <p className="text-sm text-gray-500 mt-1">
            dari {data.sales.total_tickets} tiket ({data.sales.sales_percentage}%)
          </p>
        </Card>
        <Card>
          <h3 className="text-gray-500 text-sm">Total Revenue</h3>
          <p className="text-3xl font-bold">{formatRupiah(data.sales.total_revenue)}</p>
          <p className="text-sm text-gray-500 mt-1">
            Hari ini: {formatRupiah(data.sales.revenue_today)}
          </p>
        </Card>
        <Card>
          <h3 className="text-gray-500 text-sm">Gate Scans Today</h3>
          <p className="text-3xl font-bold">{data.gate_status.total_scans_today}</p>
          <p className="text-sm text-gray-500 mt-1">
            {data.gate_status.total_gates_active} gate aktif
          </p>
        </Card>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4 mb-6">
        <Card>
          <h2 className="text-xl font-bold mb-3">Gate Status</h2>
          {data.gate_status.gates && Object.keys(data.gate_status.gates).length > 0 ? (
            <ul className="space-y-2 text-sm">
              {Object.entries(data.gate_status.gates).map(([gate, status]) => (
                <li key={gate} className="flex items-center justify-between border-b pb-2">
                  <span>{gate}</span>
                  <span
                    className={`px-2 py-1 rounded text-xs font-medium ${
                      status === 'ACTIVE'
                        ? 'bg-green-100 text-green-700'
                        : 'bg-gray-100 text-gray-600'
                    }`}
                  >
                    {status}
                  </span>
                </li>
              ))}
            </ul>
          ) : (
            <p className="text-sm text-gray-500">Belum ada data gate.</p>
          )}
        </Card>

        <Card>
          <h2 className="text-xl font-bold mb-3">Anti-Bot Hari Ini</h2>
          <dl className="space-y-2 text-sm">
            <div className="flex justify-between">
              <dt className="text-gray-500">Request diblokir</dt>
              <dd className="font-semibold">{data.anti_bot.requests_blocked_today}</dd>
            </div>
            <div className="flex justify-between">
              <dt className="text-gray-500">IP mencurigakan</dt>
              <dd className="font-semibold">{data.anti_bot.suspicious_ips_flagged}</dd>
            </div>
            <div className="flex justify-between">
              <dt className="text-gray-500">Trigger CAPTCHA</dt>
              <dd className="font-semibold">{data.anti_bot.captcha_triggers_today}</dd>
            </div>
          </dl>
        </Card>
      </div>

      <Card>
        <h2 className="text-xl font-bold mb-3">Recent Activity</h2>
        {data.recent_activity?.length > 0 ? (
          <ul className="space-y-2">
            {data.recent_activity.map((activity, index) => (
              <li
                key={`${activity.timestamp}-${index}`}
                className="text-sm border-b pb-2 flex flex-col sm:flex-row sm:items-center sm:justify-between gap-1"
              >
                <span>{activity.description}</span>
                <span className="text-gray-500">
                  {activity.type} &bull; {formatDateTime(activity.timestamp)}
                </span>
              </li>
            ))}
          </ul>
        ) : (
          <p className="text-sm text-gray-500">Belum ada aktivitas.</p>
        )}
      </Card>
    </div>
  );
}
