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
// The API binds scanner_device_id to a UUID; the human-readable label is
// kept separate so the request cannot fail field validation.
const SCANNER_DEVICE_LABEL = "dev-1";
// Must be a real 8-4-4-4-12 UUID: the previous value was missing the fourth
// group (31 chars), so gin rejected every scan body with "invalid UUID length".
const SCANNER_DEVICE_ID = "00000000-0000-4000-8000-000000000001";

export default function GateScanner() {
  const router = useRouter();
  const [ticketId, setTicketId] = useState("");
  // A dynamic QR encodes ticket UUID + HMAC signature, so the payload cannot be
  // the same string as the ticket id input.
  const [qrData, setQrData] = useState("");
  const [pin, setPin] = useState("");
  const [mode, setMode] = useState<ScanMode>("QR");
  const [result, setResult] = useState<ScanResult | null>(null);
  const [error, setError] = useState("");
  const [simulating, setSimulating] = useState(false);

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
      payload.qr_data = qrData.trim() || ticketId.trim();
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

  // Simulation path: builds a syntactically valid payload without a physical
  // scanner. The signature is intentionally fake, so the backend will normally
  // answer INVALID (red) — that is the expected behaviour of a simulation.
  const handleSimulateScan = async () => {
    setError("");
    setResult(null);
    setSimulating(true);
    const simulatedTicket = ticketId.trim() || createIdempotencyKey();
    const scanId = createIdempotencyKey();
    try {
      const response = await apiClient.post<{ data: ScanResult }>(
        "/gate/scan-qr",
        {
          ticket_id: simulatedTicket,
          qr_data: `${simulatedTicket}.simulated-${scanId}`,
          gate_id: GATE_ID,
          scanner_device_id: SCANNER_DEVICE_ID,
          scan_id: scanId,
        },
        { headers: { "X-Idempotency-Key": scanId } }
      );
      setResult(response.data.data);
    } catch (err) {
      setError(getApiErrorMessage(err, "Simulated scan ditolak backend."));
    } finally {
      setSimulating(false);
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
              {GATE_ID} &bull; {SCANNER_DEVICE_LABEL}
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
            {mode === "QR" && (
              <div>
                <label
                  htmlFor="qr-data"
                  className="block text-sm font-medium text-[#171717] mb-1.5"
                >
                  QR Data (payload)
                </label>
                <input
                  id="qr-data"
                  value={qrData}
                  onChange={(event) => setQrData(event.target.value)}
                  placeholder="Tempel hasil pindai QR"
                  autoComplete="off"
                  className="w-full border border-[#E2E8F0] rounded-lg px-4 py-2.5 text-sm focus:outline-none focus:ring-2 focus:ring-[#D08080] focus:border-transparent transition-all"
                />
                <p className="mt-1 text-[11px] text-[#4D4D4D]">
                  Kosongkan untuk memakai Ticket ID sebagai payload.
                </p>
              </div>
            )}
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

          <button
            type="button"
            onClick={() => void handleSimulateScan()}
            disabled={simulating}
            className="mt-2 w-full py-2.5 rounded-lg text-sm font-semibold border border-dashed border-[#D08080] text-[#8a4f4f] hover:bg-[#D08080]/10 transition-colors disabled:opacity-50"
          >
            {simulating ? "Mensimulasikan..." : "Simulate QR Scan"}
          </button>
          <p className="mt-1 text-[11px] text-[#4D4D4D]">
            Payload dummy (ticket_id UUID + scan_id acak) ke POST /gate/scan-qr.
          </p>

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
