'use client';

import { Suspense, useCallback, useEffect, useRef, useState } from 'react';
import { useParams, useRouter, useSearchParams } from 'next/navigation';
import {
  apiClient,
  createIdempotencyKey,
  getApiErrorMessage,
} from '@/lib/api/client';
import { Alert } from '@/components/ui/Alert';
import { Button } from '@/components/ui/Button';
import { Card } from '@/components/ui/Card';
import { Input } from '@/components/ui/Input';

interface QueueStatus {
  position: number;
  total_in_queue: number;
  estimated_wait_seconds: number;
  tier: string;
  tier_label: string;
  loyalty_score: number;
  queue_token?: string;
  challenge_required: boolean;
}

/**
 * The queue token is a bearer-like credential for the seat-lock endpoint
 * (API CONTRACT §9.1 requires X-Queue-Token), so it is kept in
 * sessionStorage instead of the URL (URLs leak through referrers and history).
 */
const QUEUE_TOKEN_STORAGE_KEY = 'e-tix-flash:queue-token';

function saveQueueToken(eventId: string, token: string): void {
  if (typeof window === 'undefined') return;
  window.sessionStorage.setItem(
    `${QUEUE_TOKEN_STORAGE_KEY}:${eventId}`,
    token
  );
}

/**
 * Waiting Room (HALAMAN 3.1 + 3.2 CAPTCHA modal).
 * Backed by GET /queue/status (API CONTRACT §8.4) and, when the fan score is
 * below the challenge threshold, POST /queue/verify-captcha (§8.5).
 */
function WaitingRoomContent() {
  const params = useParams<{ eventId: string }>();
  const searchParams = useSearchParams();
  const router = useRouter();

  const eventId = params?.eventId ?? '';
  const sectionId = searchParams.get('sectionId');

  const [status, setStatus] = useState<QueueStatus | null>(null);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(true);
  const [challengeOpen, setChallengeOpen] = useState(false);
  const [challengeId, setChallengeId] = useState('');
  const [solution, setSolution] = useState('test_pass');
  const [verifying, setVerifying] = useState(false);

  const redirectedRef = useRef(false);
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const goToSeatMap = useCallback(
    (token: string) => {
      if (redirectedRef.current) return;
      redirectedRef.current = true;
      if (pollRef.current) {
        clearInterval(pollRef.current);
        pollRef.current = null;
      }
      saveQueueToken(eventId, token);
      if (!sectionId) {
        router.push(`/customer/events/${eventId}`);
        return;
      }
      router.push(
        `/customer/seat-map/${sectionId}?eventId=${encodeURIComponent(eventId)}`
      );
    },
    [eventId, router, sectionId]
  );

  const fetchStatus = useCallback(async () => {
    if (!eventId) return;
    try {
      const response = await apiClient.get<{ data: QueueStatus }>(
        '/queue/status',
        { params: { event_id: eventId } }
      );
      const data = response.data.data;
      setStatus(data);
      setError('');
      if (data.queue_token) {
        goToSeatMap(data.queue_token);
      }
    } catch (err) {
      setError(getApiErrorMessage(err, 'Gagal cek antrean'));
    } finally {
      setLoading(false);
    }
  }, [eventId, goToSeatMap]);

  useEffect(() => {
    if (!eventId) return;
    void fetchStatus();
    pollRef.current = setInterval(() => {
      void fetchStatus();
    }, 5000);
    return () => {
      if (pollRef.current) clearInterval(pollRef.current);
      pollRef.current = null;
    };
  }, [eventId, fetchStatus]);

  useEffect(() => {
    if (!challengeId) setChallengeId(createIdempotencyKey());
  }, [challengeId]);

  const handleVerifyCaptcha = async () => {
    setVerifying(true);
    setError('');
    try {
      const response = await apiClient.post<{
        data: { verified: boolean; queue_token: string };
      }>(
        '/queue/verify-captcha',
        { challenge_id: challengeId, solution },
        { headers: { 'X-Idempotency-Key': createIdempotencyKey() } }
      );
      const token = response.data.data.queue_token;
      if (token) {
        goToSeatMap(token);
      } else {
        setError('Verifikasi CAPTCHA tidak menghasilkan token antrean');
      }
    } catch (err) {
      setError(getApiErrorMessage(err, 'Verifikasi CAPTCHA gagal'));
    } finally {
      setVerifying(false);
    }
  };

  const challengeRequired = status?.challenge_required ?? false;

  return (
    <div className="min-h-screen flex items-center justify-center bg-gray-50 px-4">
      <Card className="w-full max-w-md">
        <h2 className="text-2xl font-bold text-gray-900 text-center mb-1">
          Waiting Room
        </h2>
        <p className="text-center text-sm text-gray-500 mb-6">
          Anda antre untuk mendapatkan akses memilih kursi.
        </p>

        {error && (
          <Alert variant="error" className="mb-4">
            {error}
          </Alert>
        )}

        {loading && !status && (
          <p className="text-center text-gray-500 py-6">
            Menghubungkan ke antrean...
          </p>
        )}

        {status && (
          <div className="space-y-3">
            <div className="flex justify-between">
              <span className="text-gray-600">Posisi Anda</span>
              <span className="font-bold text-gray-900">
                #{status.position}
              </span>
            </div>
            <div className="flex justify-between">
              <span className="text-gray-600">Total antrean</span>
              <span className="font-bold text-gray-900">
                {status.total_in_queue}
              </span>
            </div>
            <div className="flex justify-between">
              <span className="text-gray-600">Estimasi tunggu</span>
              <span className="font-bold text-gray-900">
                {Math.max(0, Math.round(status.estimated_wait_seconds / 60))}{' '}
                menit
              </span>
            </div>
            <div className="flex justify-between">
              <span className="text-gray-600">Tier</span>
              <span className="font-bold text-gray-900">
                {status.tier} ({status.tier_label})
              </span>
            </div>
            <div className="flex justify-between">
              <span className="text-gray-600">Skor loyalitas</span>
              <span className="font-bold text-gray-900">
                {status.loyalty_score}
              </span>
            </div>

            <div className="w-full bg-gray-200 rounded-full h-2.5 mt-4">
              <div
                className="bg-blue-600 h-2.5 rounded-full transition-all"
                style={{
                  width: `${Math.min(
                    100,
                    Math.max(
                      5,
                      status.total_in_queue > 0
                        ? 100 - (status.position / status.total_in_queue) * 100
                        : 100
                    )
                  )}%`,
                }}
              />
            </div>

            {challengeRequired && (
              <div className="mt-4 border-t border-gray-200 pt-4">
                <Alert variant="warning" className="mb-3">
                  Skor loyalitas Anda di bawah batas, jadi verifikasi CAPTCHA
                  diperlukan sebelum memilih kursi.
                </Alert>
                {!challengeOpen ? (
                  <Button
                    className="w-full"
                    onClick={() => setChallengeOpen(true)}
                  >
                    Verifikasi CAPTCHA
                  </Button>
                ) : (
                  <div className="space-y-3">
                    <Input
                      label="Challenge ID"
                      value={challengeId}
                      onChange={(e) => setChallengeId(e.target.value)}
                    />
                    <Input
                      label="Solusi CAPTCHA"
                      value={solution}
                      onChange={(e) => setSolution(e.target.value)}
                    />
                    <p className="text-xs text-gray-500">
                      Backend lokal memakai mock captcha: solusi
                      &apos;test_pass&apos;.
                    </p>
                    <Button
                      className="w-full"
                      onClick={handleVerifyCaptcha}
                      isLoading={verifying}
                    >
                      Kirim Verifikasi
                    </Button>
                  </div>
                )}
              </div>
            )}
          </div>
        )}

        <div className="mt-6 text-center">
          <Button href="/customer/dashboard" variant="outline">
            Kembali ke Katalog
          </Button>
        </div>
      </Card>
    </div>
  );
}

export default function WaitingRoomPage() {
  return (
    <Suspense
      fallback={
        <div className="min-h-screen flex items-center justify-center bg-gray-50">
          <p className="text-gray-500">Memuat waiting room...</p>
        </div>
      }
    >
      <WaitingRoomContent />
    </Suspense>
  );
}
