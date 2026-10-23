"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { Alert } from "@/components/ui/Alert";
import { Button } from "@/components/ui/Button";
import { Card } from "@/components/ui/Card";
import { Input } from "@/components/ui/Input";
import {
  apiClient,
  createIdempotencyKey,
  getApiErrorMessage,
} from "@/lib/api/client";

type ScanMode = "QR" | "PIN";

interface ScanResult {
  valid: boolean;
  ticket_id: string;
  owner_name?: string;
  seat?: string;
  status?: string;
  scanned_at?: string;
  scan_method?: string;
}

interface ScanPayload {
  ticket_id: string;
  qr_data?: string;
  pin?: string;
  gate_id: string;
  scanner_device_id: string;
  scan_id: string;
}

const GATE_ID = "Gate A";
const SCANNER_DEVICE_ID = "dev-1";

export default function GateScanner() {
  const router = useRouter();
  const [ticketId, setTicketId] = useState("");
  const [pin, setPin] = useState("");
  const [mode, setMode] = useState<ScanMode>("QR");
  const [result, setResult] = useState<ScanResult | null>(null);
  const [error, setError] = useState("");

  const handleScan = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setError("");
    setResult(null);

    const scanId = createIdempotencyKey();
    const payload: ScanPayload = {
      ticket_id: ticketId.trim(),
      gate_id: GATE_ID,
      scanner_device_id: SCANNER_DEVICE_ID,
      scan_id: scanId,
    };

    if (mode === "QR") {
      payload.qr_data = ticketId.trim();
    } else {
      payload.pin = pin.trim().toUpperCase();
    }

    try {
      const endpoint = mode === "QR" ? "/gate/scan-qr" : "/gate/verify-pin";
      const response = await apiClient.post<{ data: ScanResult }>(
        endpoint,
        payload,
        { headers: { "X-Idempotency-Key": scanId } }
      );
      setResult(response.data.data);
    } catch (err) {
      setError(
        getApiErrorMessage(
          err,
          mode === "QR" ? "Invalid QR / ticket" : "Invalid PIN"
        )
      );
    }
  };

  return (
    <div className="min-h-screen bg-gray-900 text-white p-6">
      <div className="max-w-md mx-auto">
        <div className="flex justify-between items-center gap-3 mb-6">
          <div>
            <h1 className="text-2xl font-bold">Gate Scanner</h1>
            <p className="text-xs text-gray-400">
              {GATE_ID} &bull; {SCANNER_DEVICE_ID}
            </p>
          </div>
          <Button
            variant="outline"
            className="text-black bg-white"
            onClick={() => router.push("/gate/sync")}
          >
            Offline Sync
          </Button>
        </div>

        <div className="flex gap-2 mb-6">
          <Button
            className="flex-1"
            variant={mode === "QR" ? "primary" : "secondary"}
            onClick={() => {
              setMode("QR");
              setResult(null);
              setError("");
            }}
          >
            QR Scan
          </Button>
          <Button
            className="flex-1"
            variant={mode === "PIN" ? "primary" : "secondary"}
            onClick={() => {
              setMode("PIN");
              setResult(null);
              setError("");
            }}
          >
            PIN Entry
          </Button>
        </div>

        <Card className="p-6 text-black">
          <form onSubmit={handleScan} className="space-y-4">
            <Input
              label="Ticket ID"
              value={ticketId}
              onChange={(event) => setTicketId(event.target.value)}
              placeholder="uuid-ticket-123"
              autoComplete="off"
              required
            />
            {mode === "PIN" && (
              <Input
                label="Fallback PIN (12 characters)"
                value={pin}
                onChange={(event) => setPin(event.target.value)}
                maxLength={12}
                placeholder="A3F8K9L2M1N4"
                autoComplete="one-time-code"
                required
              />
            )}
            <Button type="submit" className="w-full">
              Validate
            </Button>
          </form>

          {error && (
            <Alert variant="error" className="mt-4 text-center font-bold">
              {error}
            </Alert>
          )}

          {result && (
            <Alert
              variant={result.valid ? "success" : "error"}
              className="mt-4 text-center"
            >
              <p className="font-bold">{result.valid ? "VALID" : "INVALID"}</p>
              {result.valid && (
                <p className="text-sm">
                  {result.owner_name ?? "-"} - {result.seat ?? "-"}
                </p>
              )}
              {result.status && (
                <p className="text-xs mt-1">Status: {result.status}</p>
              )}
            </Alert>
          )}
        </Card>
      </div>
    </div>
  );
}
