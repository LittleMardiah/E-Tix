'use client';

import { useEffect, useState } from 'react';
import { useParams } from 'next/navigation';
import { apiClient, getApiErrorMessage } from '@/lib/api/client';
import { Alert } from '@/components/ui/Alert';
import { Button } from '@/components/ui/Button';
import { Card } from '@/components/ui/Card';

interface GateUtilization {
  total_scans: number;
  valid_scans: number;
  utilization: number;
}

interface ScanTimelineBucket {
  hour: string;
  scans: number;
}

interface AnalyticsData {
  event_id: string;
  event_name: string;
  total_tickets: number;
  tickets_sold: number;
  attendance_count: number;
  attendance_rate: number;
  no_show_rate: number;
  peak_entry_time: string;
  gate_utilization: Record<string, GateUtilization>;
  scan_timeline: ScanTimelineBucket[];
  loyal_customers: number;
  new_customers: number;
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

export default function EOAnalytics() {
  const params = useParams<{ eventId: string }>();
  const eventId = params?.eventId ?? '';

  const [data, setData] = useState<AnalyticsData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  useEffect(() => {
    if (!eventId) return;
    let cancelled = false;

    setLoading(true);
    setError('');
    apiClient
      .get<{ data: AnalyticsData }>(`/events/${eventId}/analytics`)
      .then((response) => {
        if (!cancelled) setData(response.data.data);
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setError(getApiErrorMessage(err, 'Gagal memuat analytics event'));
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
    return <div className="p-10 text-center text-gray-500">Memuat analytics...</div>;
  }

  if (error || !data) {
    return (
      <div className="p-10 max-w-6xl mx-auto">
        <Alert variant="error" title="Gagal memuat analytics">
          {error || 'Data analytics tidak tersedia.'}
        </Alert>
      </div>
    );
  }

  const maxScans = Math.max(...data.scan_timeline.map((bucket) => bucket.scans), 1);

  return (
    <div className="p-6 md:p-10 max-w-6xl mx-auto">
      <div className="flex flex-col gap-4 md:flex-row md:items-start md:justify-between mb-6">
        <div>
          <h1 className="text-3xl font-bold">Analytics: {data.event_name}</h1>
          <p className="text-sm text-gray-500 mt-1">
            Dibuat {formatDateTime(data.generated_at)}
          </p>
        </div>
        <Button href={`/eo/post-event/${data.event_id}`}>Post-Event Report</Button>
      </div>

      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4 mb-6">
        <Card>
          <h3 className="text-gray-500 text-sm">Attendance Rate</h3>
          <p className="text-2xl font-bold">{data.attendance_rate}%</p>
          <p className="text-sm text-gray-500 mt-1">
            {data.attendance_count} dari {data.tickets_sold} tiket terjual
          </p>
        </Card>
        <Card>
          <h3 className="text-gray-500 text-sm">No-Show Rate</h3>
          <p className="text-2xl font-bold">{data.no_show_rate}%</p>
        </Card>
        <Card>
          <h3 className="text-gray-500 text-sm">Peak Entry</h3>
          <p className="text-2xl font-bold">{data.peak_entry_time || '-'}</p>
        </Card>
        <Card>
          <h3 className="text-gray-500 text-sm">Tiket</h3>
          <p className="text-2xl font-bold">{data.tickets_sold}</p>
          <p className="text-sm text-gray-500 mt-1">
            dari {data.total_tickets} total
          </p>
        </Card>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4 mb-6">
        <Card>
          <h2 className="text-xl font-bold mb-3">Gate Utilization</h2>
          {Object.keys(data.gate_utilization).length > 0 ? (
            <div className="space-y-3">
              {Object.entries(data.gate_utilization).map(([gate, metrics]) => (
                <div key={gate}>
                  <div className="flex items-center justify-between text-sm mb-1">
                    <span className="font-medium">{gate}</span>
                    <span className="text-gray-500">
                      {metrics.utilization}% ({metrics.valid_scans}/{metrics.total_scans})
                    </span>
                  </div>
                  <div className="h-2 bg-gray-100 rounded">
                    <div
                      className="h-2 bg-blue-600 rounded"
                      style={{ width: `${Math.min(metrics.utilization, 100)}%` }}
                    />
                  </div>
                </div>
              ))}
            </div>
          ) : (
            <p className="text-sm text-gray-500">Belum ada data gate.</p>
          )}
        </Card>

        <Card>
          <h2 className="text-xl font-bold mb-3">Scan Timeline</h2>
          {data.scan_timeline.length > 0 ? (
            <div className="space-y-2">
              {data.scan_timeline.map((bucket) => (
                <div key={bucket.hour} className="flex items-center gap-3 text-sm">
                  <span className="w-14 text-gray-500">{bucket.hour}</span>
                  <div className="flex-1 h-3 bg-gray-100 rounded">
                    <div
                      className="h-3 bg-indigo-500 rounded"
                      style={{ width: `${(bucket.scans / maxScans) * 100}%` }}
                    />
                  </div>
                  <span className="w-12 text-right font-medium">{bucket.scans}</span>
                </div>
              ))}
            </div>
          ) : (
            <p className="text-sm text-gray-500">Belum ada data scan.</p>
          )}
        </Card>
      </div>

      <Card>
        <h2 className="text-xl font-bold mb-3">Customer Segments</h2>
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4 text-sm">
          <div className="flex items-center justify-between border-b pb-2">
            <span className="text-gray-500">Loyal customers</span>
            <span className="font-semibold">{data.loyal_customers}</span>
          </div>
          <div className="flex items-center justify-between border-b pb-2">
            <span className="text-gray-500">New customers</span>
            <span className="font-semibold">{data.new_customers}</span>
          </div>
        </div>
      </Card>
    </div>
  );
}
