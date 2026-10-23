"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { Alert } from "@/components/ui/Alert";
import { Button } from "@/components/ui/Button";
import { Card } from "@/components/ui/Card";
import {
  apiClient,
  createIdempotencyKey,
  getApiErrorMessage,
} from "@/lib/api/client";

const GATE_ID = "Gate A";
const SCANNER_DEVICE_ID = "dev-1";

interface SyncError {
  scan_id?: string;
  message?: string;
  reason?: string;
}

interface SyncResult {
  synced: number;
  duplicates: number;
  failed: number;
  errors?: SyncError[];
}

export default function GateSync() {
  const router = useRouter();
  const [syncing, setSyncing] = useState(false);
  const [result, setResult] = useState<SyncResult | null>(null);
  const [error, setError] = useState("");

  const handleSync = async () => {
    setSyncing(true);
    setError("");
    setResult(null);

    const scanId = createIdempotencyKey();
    const payload = {
      device_id: SCANNER_DEVICE_ID,
      scans: [
        {
          scan_id: scanId,
          ticket_id: createIdempotencyKey(),
          gate_id: GATE_ID,
          scanned_at: new Date().toISOString(),
          scan_method: "QR" as const,
          scan_status: "VALID" as const,
        },
      ],
    };

    try {
      const response = await apiClient.post<{ data: SyncResult }>(
        "/gate/sync",
        payload,
        { headers: { "X-Idempotency-Key": createIdempotencyKey() } }
      );
      setResult(response.data.data);
    } catch (err) {
      setError(getApiErrorMessage(err, "Sync failed"));
    } finally {
      setSyncing(false);
    }
  };

  return (
    <div className="min-h-screen bg-gray-900 text-white p-6">
      <div className="max-w-md mx-auto">
        <div className="flex justify-between items-center gap-3 mb-6">
          <div>
            <h1 className="text-2xl font-bold">Offline Sync</h1>
            <p className="text-xs text-gray-400">
              {GATE_ID} &bull; {SCANNER_DEVICE_ID}
            </p>
          </div>
          <Button
            variant="outline"
            className="text-black bg-white"
            onClick={() => router.push("/gate/scanner")}
          >
            Back
          </Button>
        </div>

        <Card className="p-6 text-black">
          <p className="mb-2 text-lg text-center">
            Koneksi terputus? Sinkronkan scan offline sekarang.
          </p>
          <p className="mb-5 text-xs text-gray-500 text-center">
            Mode demo: satu scan contoh akan dikirim ke server.
          </p>
          <Button onClick={handleSync} isLoading={syncing} className="w-full">
            Sync Now
          </Button>

          {error && (
            <Alert variant="error" className="mt-4">
              {error}
            </Alert>
          )}

          {result && (
            <Alert variant="success" className="mt-4 text-left">
              <p className="font-semibold">Sync complete</p>
              <p>Synced: {result.synced}</p>
              <p>Duplicates: {result.duplicates}</p>
              <p>Failed: {result.failed}</p>
              {result.errors && result.errors.length > 0 && (
                <ul className="mt-2 list-disc list-inside text-xs">
                  {result.errors.map((item, index) => (
                    <li key={`${item.scan_id ?? "error"}-${index}`}>
                      {item.scan_id ?? "scan"}:{" "}
                      {item.message ?? item.reason ?? "unknown error"}
                    </li>
                  ))}
                </ul>
              )}
            </Alert>
          )}
        </Card>
      </div>
    </div>
  );
}
