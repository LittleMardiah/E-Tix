'use client';

import { useCallback, useEffect, useState } from 'react';
import { useParams } from 'next/navigation';
import { apiClient, getApiErrorMessage } from '@/lib/api/client';
import { Alert } from '@/components/ui/Alert';
import { Button } from '@/components/ui/Button';
import { Card } from '@/components/ui/Card';
import { formatRupiah } from '@/lib/format';

interface ReportSummary {
  total_revenue: string;
  average_ticket_price: string;
  platform_fee_earned: string;
  refund_amount: string;
}

interface ReportAttendance {
  total_tickets: number;
  used_tickets: number;
  unused_tickets: number;
  no_show_rate: number;
  resale_count: number;
  resale_revenue: string;
}

interface ReportAntiBot {
  total_requests_blocked: number;
  bot_filtering_rate: number;
  suspicious_ips_flagged: number;
  captcha_triggers: number;
}

interface ReportGatePerformance {
  total_scans: number;
  valid_scans: number;
  invalid_scans: number;
}

interface ReportInsights {
  recommendations: string[];
}

interface PostEventData {
  event_id: string;
  event_name: string;
  summary: ReportSummary;
  attendance: ReportAttendance;
  anti_bot: ReportAntiBot;
  gate_performance: Record<string, ReportGatePerformance>;
  insights: ReportInsights;
  generated_at: string;
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

export default function EOPostEventReport() {
  const params = useParams<{ eventId: string }>();
  const eventId = params?.eventId ?? '';

  const [data, setData] = useState<PostEventData | null>(null);
  const [loading, setLoading] = useState(true);
  const [downloading, setDownloading] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    if (!eventId) return;
    let cancelled = false;

    setLoading(true);
    setError('');
    apiClient
      .get<{ data: PostEventData }>(`/events/${eventId}/post-event-report`, {
        params: { format: 'json' },
      })
      .then((response) => {
        if (!cancelled) setData(response.data.data);
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setError(getApiErrorMessage(err, 'Gagal memuat post-event report'));
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });

    return () => {
      cancelled = true;
    };
  }, [eventId]);

  const downloadCsv = useCallback(async () => {
    if (!eventId) return;
    setDownloading(true);
    setError('');
    try {
      const response = await apiClient.get(
        `/events/${eventId}/post-event-report`,
        { params: { format: 'csv' }, responseType: 'blob' }
      );
      const url = URL.createObjectURL(response.data as Blob);
      const link = document.createElement('a');
      link.href = url;
      link.download = `post-event-${eventId}.csv`;
      document.body.appendChild(link);
      link.click();
      link.remove();
      URL.revokeObjectURL(url);
    } catch (err: unknown) {
      setError(getApiErrorMessage(err, 'Gagal mengunduh CSV'));
    } finally {
      setDownloading(false);
    }
  }, [eventId]);

  if (!eventId) {
    return (
      <div className="p-10 max-w-6xl mx-auto">
        <Alert variant="error">Event ID tidak valid.</Alert>
      </div>
    );
  }

  if (loading) {
    return <div className="p-10 text-center text-gray-500">Memuat report...</div>;
  }

  if (error && !data) {
    return (
      <div className="p-10 max-w-6xl mx-auto">
        <Alert variant="error" title="Gagal memuat report">
          {error}
        </Alert>
      </div>
    );
  }

  if (!data) {
    return (
      <div className="p-10 max-w-6xl mx-auto">
        <Alert variant="info">Report tidak tersedia.</Alert>
      </div>
    );
  }

  return (
    <div className="p-6 md:p-10 max-w-6xl mx-auto">
      <div className="flex flex-col gap-4 md:flex-row md:items-start md:justify-between mb-6">
        <div>
          <h1 className="text-3xl font-bold">Post-Event: {data.event_name}</h1>
          <p className="text-sm text-gray-500 mt-1">
            Dibuat {formatDateTime(data.generated_at)}
          </p>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button href={`/eo/analytics/${data.event_id}`} variant="outline">
            Analytics
          </Button>
          <Button onClick={downloadCsv} isLoading={downloading}>
            Download CSV
          </Button>
        </div>
      </div>

      {error && (
        <Alert variant="error" className="mb-6">
          {error}
        </Alert>
      )}

      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4 mb-6">
        <Card>
          <h3 className="text-gray-500 text-sm">Total Revenue</h3>
          <p className="text-2xl font-bold">{formatRupiah(data.summary.total_revenue)}</p>
        </Card>
        <Card>
          <h3 className="text-gray-500 text-sm">Average Ticket Price</h3>
          <p className="text-2xl font-bold">
            {formatRupiah(data.summary.average_ticket_price)}
          </p>
        </Card>
        <Card>
          <h3 className="text-gray-500 text-sm">Platform Fee</h3>
          <p className="text-2xl font-bold">
            {formatRupiah(data.summary.platform_fee_earned)}
          </p>
        </Card>
        <Card>
          <h3 className="text-gray-500 text-sm">Refund</h3>
          <p className="text-2xl font-bold">{formatRupiah(data.summary.refund_amount)}</p>
        </Card>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4 mb-6">
        <Card>
          <h2 className="text-xl font-bold mb-3">Attendance</h2>
          <dl className="space-y-2 text-sm">
            <div className="flex justify-between">
              <dt className="text-gray-500">Total tickets</dt>
              <dd className="font-semibold">{data.attendance.total_tickets}</dd>
            </div>
            <div className="flex justify-between">
              <dt className="text-gray-500">Used</dt>
              <dd className="font-semibold">{data.attendance.used_tickets}</dd>
            </div>
            <div className="flex justify-between">
              <dt className="text-gray-500">Unused</dt>
              <dd className="font-semibold">{data.attendance.unused_tickets}</dd>
            </div>
            <div className="flex justify-between">
              <dt className="text-gray-500">No-show rate</dt>
              <dd className="font-semibold">{data.attendance.no_show_rate}%</dd>
            </div>
            <div className="flex justify-between">
              <dt className="text-gray-500">Resale</dt>
              <dd className="font-semibold">
                {data.attendance.resale_count} &bull;{' '}
                {formatRupiah(data.attendance.resale_revenue)}
              </dd>
            </div>
          </dl>
        </Card>

        <Card>
          <h2 className="text-xl font-bold mb-3">Anti-Bot</h2>
          <dl className="space-y-2 text-sm">
            <div className="flex justify-between">
              <dt className="text-gray-500">Requests blocked</dt>
              <dd className="font-semibold">{data.anti_bot.total_requests_blocked}</dd>
            </div>
            <div className="flex justify-between">
              <dt className="text-gray-500">Filtering rate</dt>
              <dd className="font-semibold">{data.anti_bot.bot_filtering_rate}%</dd>
            </div>
            <div className="flex justify-between">
              <dt className="text-gray-500">Suspicious IPs</dt>
              <dd className="font-semibold">{data.anti_bot.suspicious_ips_flagged}</dd>
            </div>
            <div className="flex justify-between">
              <dt className="text-gray-500">CAPTCHA triggers</dt>
              <dd className="font-semibold">{data.anti_bot.captcha_triggers}</dd>
            </div>
          </dl>
        </Card>
      </div>

      <Card className="mb-6">
        <h2 className="text-xl font-bold mb-3">Gate Performance</h2>
        {Object.keys(data.gate_performance).length > 0 ? (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b text-left text-gray-500">
                  <th className="py-2 pr-4">Gate</th>
                  <th className="py-2 pr-4">Total Scans</th>
                  <th className="py-2 pr-4">Valid</th>
                  <th className="py-2">Invalid</th>
                </tr>
              </thead>
              <tbody>
                {Object.entries(data.gate_performance).map(([gate, metrics]) => (
                  <tr key={gate} className="border-b">
                    <td className="py-2 pr-4 font-medium">{gate}</td>
                    <td className="py-2 pr-4">{metrics.total_scans}</td>
                    <td className="py-2 pr-4">{metrics.valid_scans}</td>
                    <td className="py-2">{metrics.invalid_scans}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <p className="text-sm text-gray-500">Belum ada data gate.</p>
        )}
      </Card>

      <Card>
        <h2 className="text-xl font-bold mb-3">Insights</h2>
        {data.insights.recommendations?.length > 0 ? (
          <ul className="list-disc pl-5 space-y-2 text-sm">
            {data.insights.recommendations.map((recommendation, index) => (
              <li key={`${recommendation}-${index}`}>{recommendation}</li>
            ))}
          </ul>
        ) : (
          <p className="text-sm text-gray-500">Belum ada rekomendasi.</p>
        )}
      </Card>
    </div>
  );
}
