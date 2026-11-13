"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { Alert } from "@/components/ui/Alert";
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
    <div className="min-h-screen flex flex-col items-center justify-center bg-[#FAFAFA] p-4">
      <div className="w-full max-w-md">
        <div className="flex items-center justify-between gap-3 mb-4">
          <div>
            <h1 className="text-xl font-bold tracking-tight text-[#171717]">
              Gate Scanner
            </h1>
            <p className="text-xs text-[#4D4D4D] mt-0.5">
              {GATE_ID} &bull; {SCANNER_DEVICE_ID}
            </p>
          </div>
          <button
            type="button"
            onClick={() => router.push("/gate/sync")}
            className="text-xs font-medium text-[#4D4D4D] bg-white border border-[#E2E8F0] rounded-lg px-3 py-2 hover:border-[#D08080] transition-colors"
          >
            Offline Sync
          </button>
        </div>

        <div className="flex gap-2 mb-4">
          <button
            type="button"
            className={`flex-1 py-2 rounded-lg text-sm font-medium border transition-colors ${
              mode === "QR"
                ? "bg-[#D08080] text-[#171717] border-[#D08080]"
                : "bg-white text-[#4D4D4D] border-[#E2E8F0] hover:border-[#D08080]"
            }`}
            onClick={() => {
              setMode("QR");
              setResult(null);
              setError("");
            }}
          >
            QR Scan
          </button>
          <button
            type="button"
            className={`flex-1 py-2 rounded-lg text-sm font-medium border transition-colors ${
              mode === "PIN"
                ? "bg-[#D08080] text-[#171717] border-[#D08080]"
                : "bg-white text-[#4D4D4D] border-[#E2E8F0] hover:border-[#D08080]"
            }`}
            onClick={() => {
              setMode("PIN");
              setResult(null);
              setError("");
            }}
          >
            PIN Entry
          </button>
        </div>

        <div className="bg-white border border-[#E2E8F0] rounded-2xl p-8 shadow-sm w-full max-w-md text-center">
          <form onSubmit={handleScan} className="space-y-4 text-left">
            <div>
              <label
                htmlFor="ticket-id"
                className="block text-sm font-medium text-[#171717] mb-1.5"
              >
                Ticket ID
              </label>
              <input
                id="ticket-id"
                value={ticketId}
                onChange={(event) => setTicketId(event.target.value)}
                placeholder="uuid-ticket-123"
                autoComplete="off"
                required
                className="w-full border border-[#E2E8F0] rounded-lg px-4 py-2.5 text-sm focus:outline-none focus:ring-2 focus:ring-[#D08080] focus:border-transparent transition-all"
              />
            </div>
            {mode === "PIN" && (
              <div>
                <label
                  htmlFor="ticket-pin"
                  className="block text-sm font-medium text-[#171717] mb-1.5"
                >
                  Fallback PIN (12 characters)
                </label>
                <input
                  id="ticket-pin"
                  value={pin}
                  onChange={(event) => setPin(event.target.value)}
                  maxLength={12}
                  placeholder="A3F8K9L2M1N4"
                  autoComplete="one-time-code"
                  required
                  className="w-full border border-[#E2E8F0] rounded-lg px-4 py-2.5 text-sm font-mono uppercase tracking-widest focus:outline-none focus:ring-2 focus:ring-[#D08080] focus:border-transparent transition-all"
                />
              </div>
            )}
            <button
              type="submit"
              className="bg-[#D08080] text-[#171717] w-full py-3 rounded-lg font-bold hover:bg-[#c06b6b] transition-colors"
            >
              Validate
            </button>
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
        </div>
      </div>
    </div>
  );
}
