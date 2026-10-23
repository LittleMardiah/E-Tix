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

interface AttendanceData {
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

/**
 * Attendance Report (HALAMAN 9.1).
 * Backed by GET /events/{event_id}/analytics (API CONTRACT 13.2), which
 * exposes the attendance block used by this report.
 */
export default function AttendanceReportPage() {
  const params = useParams<{ eventId: string }>();
  const eventId = params?.eventId ?? '';

  const [data, setData] = useState<AttendanceData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  useEffect(() => {
    if (!eventId) {
      setLoading(false);
      return;
    }

    let active = true;
    setLoading(true);
    setError('');
    apiClient
      .get<{ data: AttendanceData }>(`/events/${eventId}/analytics`)
      .then((response) => {
        if (active) setData(response.data.data);
      })
      .catch((err) => {
        if (active) {
          setError(getApiErrorMessage(err, 'Gagal memuat attendance report'));
        }
      })
      .finally(() => {
        if (active) setLoading(false);
      });

    return () => {
      active = false;
    };
  }, [eventId]);

  const maxScans = data
    ? Math.max(...data.scan_timeline.map((bucket) => bucket.scans), 1)
    : 1;

  return (
    <div className="p-6 md:p-10 max-w-6xl mx-auto">
      <div className="flex flex-col gap-4 md:flex-row md:items-start md:justify-between mb-6">
        <div>
          <h1 className="text-3xl font-bold">Attendance Report</h1>
          {data && (
            <p className="text-sm text-gray-500 mt-1">
              {data.event_name} &bull; dibuat {formatDateTime(data.generated_at)}
            </p>
          )}
        </div>
        <div className="flex flex-wrap gap-2">
          <Button href={`/eo/analytics/${eventId}`} variant="outline">
            Analytics
          </Button>
          <Button href={`/eo/post-event/${eventId}`} variant="outline">
            Post-Event Report
          </Button>
        </div>
      </div>

      {!eventId && <Alert variant="error">Event ID tidak valid.</Alert>}

      {eventId && loading && (
        <p className="py-10 text-center text-gray-500">
          Memuat attendance report...
        </p>
      )}

      {eventId && !loading && error && (
        <Alert variant="error" title="Gagal memuat attendance report">
          {error}
        </Alert>
      )}

      {eventId && !loading && !error && data && (
        <>
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4 mb-6">
            <Card>
              <h2 className="text-gray-500 text-sm">Total Terjual</h2>
              <p className="text-2xl font-bold">{data.tickets_sold}</p>
              <p className="text-sm text-gray-500 mt-1">
                dari {data.total_tickets} total tiket
              </p>
            </Card>
            <Card>
              <h2 className="text-gray-500 text-sm">Hadir</h2>
              <p className="text-2xl font-bold">{data.attendance_count}</p>
              <p className="text-sm text-gray-500 mt-1">tiket sudah discan</p>
            </Card>
            <Card>
              <h2 className="text-gray-500 text-sm">Attendance Rate</h2>
              <p className="text-2xl font-bold text-green-600">
                {data.attendance_rate}%
              </p>
              <div className="h-2 bg-gray-100 rounded mt-3">
                <div
                  className="h-2 bg-green-600 rounded"
                  style={{
                    width: `${Math.min(Math.max(data.attendance_rate, 0), 100)}%`,
                  }}
                />
              </div>
            </Card>
            <Card>
              <h2 className="text-gray-500 text-sm">No-Show Rate</h2>
              <p className="text-2xl font-bold">{data.no_show_rate}%</p>
              <p className="text-sm text-gray-500 mt-1">
                Peak entry: {data.peak_entry_time || '-'}
              </p>
            </Card>
          </div>

          <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
            <Card>
              <h2 className="text-xl font-bold mb-3">Gate Utilization</h2>
              {Object.keys(data.gate_utilization).length > 0 ? (
                <div className="space-y-3">
                  {Object.entries(data.gate_utilization).map(
                    ([gate, metrics]) => (
                      <div key={gate}>
                        <div className="flex items-center justify-between text-sm mb-1">
                          <span className="font-medium">{gate}</span>
                          <span className="text-gray-500">
                            {metrics.utilization}% ({metrics.valid_scans}/
                            {metrics.total_scans})
                          </span>
                        </div>
                        <div className="h-2 bg-gray-100 rounded">
                          <div
                            className="h-2 bg-blue-600 rounded"
                            style={{
                              width: `${Math.min(
                                Math.max(metrics.utilization, 0),
                                100
                              )}%`,
                            }}
                          />
                        </div>
                      </div>
                    )
                  )}
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
                    <div
                      key={bucket.hour}
                      className="flex items-center gap-3 text-sm"
                    >
                      <span className="w-14 text-gray-500">{bucket.hour}</span>
                      <div className="flex-1 h-3 bg-gray-100 rounded">
                        <div
                          className="h-3 bg-indigo-500 rounded"
                          style={{
                            width: `${(bucket.scans / maxScans) * 100}%`,
                          }}
                        />
                      </div>
                      <span className="w-12 text-right font-medium">
                        {bucket.scans}
                      </span>
                    </div>
                  ))}
                </div>
              ) : (
                <p className="text-sm text-gray-500">Belum ada data scan.</p>
              )}
            </Card>
          </div>
        </>
      )}
    </div>
  );
}
