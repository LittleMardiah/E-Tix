"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import Image from "next/image";
import { useParams, useRouter } from "next/navigation";
import { apiClient, getApiErrorMessage } from "@/lib/api/client";

type ZoneData = {
  id: string;
  name: string;
  price: number | string;
  quota?: number;
  polygon_points: string;
  pov_image_url?: string;
  distance?: string;
  color?: string;
};

type EventDetailData = {
  name?: string;
  layout_image_url?: string;
  layout_image_width?: number;
  layout_image_height?: number;
  layout_zones?: ZoneData[] | string | null;
};

const MIN_ZOOM = 1;
const MAX_ZOOM = 5;
const ZOOM_STEP = 1.25;
const DRAG_THRESHOLD = 4;

function formatPrice(value: number | string | undefined): string {
  const numeric = typeof value === "number" ? value : Number(value ?? 0);
  if (Number.isNaN(numeric)) {
    return String(value ?? "0");
  }
  return numeric.toLocaleString("id-ID");
}

function parseZones(value: EventDetailData["layout_zones"]): ZoneData[] {
  if (!value) return [];
  try {
    const parsed = typeof value === "string" ? JSON.parse(value) : value;
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    // A malformed layout must not blank the whole page.
    return [];
  }
}

function polygonCenter(pointsAttr: string): { x: number; y: number } | null {
  const vertices = pointsAttr
    .trim()
    .split(/\s+/)
    .map((pair) => pair.split(",").map(Number))
    .filter((pair) => pair.length === 2 && pair.every(Number.isFinite));
  if (vertices.length === 0) return null;
  const total = vertices.reduce(
    (acc, [x, y]) => ({ x: acc.x + x, y: acc.y + y }),
    { x: 0, y: 0 }
  );
  return { x: total.x / vertices.length, y: total.y / vertices.length };
}

function isZoneSoldOut(zone: ZoneData | null): boolean {
  if (!zone) return false;
  return typeof zone.quota === "number" && zone.quota <= 0;
}

export default function InteractiveMap() {
  const params = useParams();
  const router = useRouter();
  const eventId = Array.isArray(params?.eventId) ? params.eventId[0] : params?.eventId;
  const [selectedZone, setSelectedZone] = useState<ZoneData | null>(null);
  const [zones, setZones] = useState<ZoneData[]>([]);
  const [eventData, setEventData] = useState<EventDetailData | null>(null);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [isBuying, setIsBuying] = useState(false);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  const [zoom, setZoom] = useState(1);
  const [pan, setPan] = useState({ x: 0, y: 0 });
  const dragRef = useRef<{
    startX: number;
    startY: number;
    panX: number;
    panY: number;
    moved: boolean;
  } | null>(null);
  const movedRef = useRef(false);

  const loadMap = useCallback(
    async (mode: "initial" | "refresh" = "initial") => {
      if (!eventId) return;
      if (mode === "initial") setLoading(true);
      else setRefreshing(true);
      setError(null);
      try {
        const res = await apiClient.get(`/events/${eventId}`);
        const data: EventDetailData | null = res.data?.data ?? null;
        setEventData(data);
        const nextZones = parseZones(data?.layout_zones);
        setZones(nextZones);
        // Keep the selection in sync with the fresh quota numbers.
        setSelectedZone((current) => {
          if (!current) return null;
          const updated = nextZones.find((zone) => zone.id === current.id);
          return updated ?? current;
        });
        if (mode === "refresh") {
          setNotice(
            `Kuota diperbarui ${new Date().toLocaleTimeString("id-ID", {
              hour: "2-digit",
              minute: "2-digit",
            })}`
          );
        }
      } catch (err) {
        console.error("Gagal load map", err);
        setError(getApiErrorMessage(err, "Gagal memuat data peta."));
        if (mode === "initial") setZones([]);
      } finally {
        if (mode === "initial") setLoading(false);
        else setRefreshing(false);
      }
    },
    [eventId]
  );

  useEffect(() => {
    void loadMap("initial");
  }, [loadMap]);

  useEffect(() => {
    if (!notice) return;
    const timer = setTimeout(() => setNotice(null), 4000);
    return () => clearTimeout(timer);
  }, [notice]);

  const handleBuyTicket = async () => {
    if (!selectedZone || !eventId || isBuying) return;
    if (isZoneSoldOut(selectedZone)) return;
    setIsBuying(true);
    setErrorMsg(null);
    try {
      const res = await apiClient.post<{
        data: {
          event_id: string;
          zone_name: string;
          quantity: number;
          status: string;
          booking_id?: string;
        };
      }>("/bookings/zoned", {
        event_id: eventId,
        zone_name: selectedZone.name,
        quantity: 1,
      });
      // Zoned checkout is created as BULK + PAID and its tickets are
      // generated asynchronously by the zoned batch worker, so there is no
      // payment step to go through: land on the ticket list instead.
      const bookingId = res.data?.data?.booking_id;
      if (!bookingId) {
        setErrorMsg(
          "Tiket zona berhasil dibuat, tetapi server tidak mengembalikan booking_id."
        );
        return;
      }
      setNotice("Tiket berhasil diproses! Mengalihkan ke halaman tiket...");
      router.push("/customer/my-tickets");
    } catch (err) {
      setErrorMsg(getApiErrorMessage(err, "Gagal memproses tiket zona."));
    } finally {
      setIsBuying(false);
    }
  };

  const handlePointerDown = (event: React.PointerEvent<HTMLDivElement>) => {
    if (event.button !== 0) return;
    dragRef.current = {
      startX: event.clientX,
      startY: event.clientY,
      panX: pan.x,
      panY: pan.y,
      moved: false,
    };
    movedRef.current = false;
  };

  const handlePointerMove = (event: React.PointerEvent<HTMLDivElement>) => {
    const drag = dragRef.current;
    if (!drag) return;
    // The pointer was released outside the container, or we are hovering.
    if (event.buttons === 0) {
      dragRef.current = null;
      return;
    }
    const dx = event.clientX - drag.startX;
    const dy = event.clientY - drag.startY;
    if (Math.abs(dx) > DRAG_THRESHOLD || Math.abs(dy) > DRAG_THRESHOLD) {
      drag.moved = true;
      movedRef.current = true;
    }
    if (drag.moved) setPan({ x: drag.panX + dx, y: drag.panY + dy });
  };

  const endDrag = () => {
    dragRef.current = null;
  };

  const selectZone = (zone: ZoneData) => {
    // A pan gesture must not be interpreted as a zone selection.
    if (movedRef.current) return;
    setSelectedZone(zone);
  };

  const viewBoxWidth = eventData?.layout_image_width || 800;
  const viewBoxHeight = eventData?.layout_image_height || 600;
  const mapImage = eventData?.layout_image_url?.trim() ? eventData.layout_image_url : null;
  const selectedQuota = typeof selectedZone?.quota === "number" ? selectedZone.quota : 0;
  const selectedSoldOut = isZoneSoldOut(selectedZone);
  const soldOutLabelSize = Math.max(14, viewBoxWidth / 32);

  if (loading) {
    return (
      <div className="max-w-7xl mx-auto px-4 py-8">
        <p className="text-[#4D4D4D]">Memuat peta...</p>
      </div>
    );
  }

  return (
    <div className="max-w-7xl mx-auto px-4 py-8">
      <div className="bg-[#0B0F19] text-white rounded-2xl p-4 md:p-8 font-sans">
        <h1 className="text-2xl font-bold mb-6 text-center md:text-left">Pilih Area Konser - {eventData?.name ?? eventId}</h1>

        {error && (
          <div className="max-w-7xl mx-auto mb-4 rounded-xl border border-red-500/40 bg-red-500/10 px-4 py-3 text-sm text-red-300">
            {error}
          </div>
        )}

        {notice && (
          <div
            role="status"
            className="max-w-7xl mx-auto mb-4 rounded-xl border border-green-500/40 bg-green-500/10 px-4 py-3 text-sm text-green-300"
          >
            {notice}
          </div>
        )}

        <div className="flex flex-col md:flex-row gap-8 max-w-7xl mx-auto">
          {/* MAP SECTION */}
          <div className="w-full md:flex-1">
            <div className="mb-2 flex flex-wrap items-center gap-2">
              <button
                type="button"
                onClick={() => setZoom((current) => Math.min(MAX_ZOOM, current * ZOOM_STEP))}
                disabled={zoom >= MAX_ZOOM}
                aria-label="Zoom in"
                className="h-9 w-9 rounded-lg border border-zinc-700 text-lg font-bold hover:border-[#D08080] disabled:opacity-40"
              >
                +
              </button>
              <button
                type="button"
                onClick={() => setZoom((current) => Math.max(MIN_ZOOM, current / ZOOM_STEP))}
                disabled={zoom <= MIN_ZOOM}
                aria-label="Zoom out"
                className="h-9 w-9 rounded-lg border border-zinc-700 text-lg font-bold hover:border-[#D08080] disabled:opacity-40"
              >
                -
              </button>
              <button
                type="button"
                onClick={() => {
                  setZoom(1);
                  setPan({ x: 0, y: 0 });
                }}
                className="h-9 rounded-lg border border-zinc-700 px-3 text-sm hover:border-[#D08080]"
              >
                Reset
              </button>
              <span className="text-xs text-zinc-400">{Math.round(zoom * 100)}%</span>
              <button
                type="button"
                onClick={() => void loadMap("refresh")}
                disabled={refreshing}
                className="ml-auto h-9 rounded-lg border border-zinc-700 px-3 text-sm font-medium hover:border-[#D08080] disabled:opacity-50"
              >
                {refreshing ? "Memuat..." : "Refresh Kuota"}
              </button>
            </div>

            <div
              className="relative w-full overflow-hidden rounded-2xl border border-[#E2E8F0] shadow-sm bg-gray-100 touch-none select-none"
              style={{ aspectRatio: `${viewBoxWidth} / ${viewBoxHeight}` }}
              onPointerDown={handlePointerDown}
              onPointerMove={handlePointerMove}
              onPointerUp={endDrag}
              onPointerCancel={endDrag}
            >
              <div
                className="absolute inset-0"
                style={{
                  transform: `translate(${pan.x}px, ${pan.y}px) scale(${zoom})`,
                  transformOrigin: "0 0",
                }}
              >
                {mapImage ? (
                  <Image
                    src={mapImage}
                    alt="Venue Map"
                    fill
                    unoptimized
                    draggable={false}
                    sizes="(max-width: 768px) 100vw, 70vw"
                    className="object-cover select-none"
                  />
                ) : (
                  <div className="absolute inset-0 flex w-full items-center justify-center bg-gray-100 px-6 text-center text-gray-500">
                    Peta tidak tersedia untuk event ini
                  </div>
                )}
                <svg
                  viewBox={`0 0 ${viewBoxWidth} ${viewBoxHeight}`}
                  className="absolute inset-0 h-full w-full"
                  preserveAspectRatio="none"
                >
                  {zones.map((zone) => {
                    const soldOut = isZoneSoldOut(zone);
                    const center = soldOut ? polygonCenter(zone.polygon_points) : null;
                    const selected = selectedZone?.id === zone.id;
                    return (
                      <g key={zone.id}>
                        <polygon
                          points={zone.polygon_points}
                          onClick={() => selectZone(zone)}
                          className={`transition-all duration-300 cursor-pointer ${
                            soldOut
                              ? "fill-gray-500/40 stroke-gray-400 stroke-2"
                              : selected
                                ? "fill-[#D08080]/60 stroke-white stroke-2"
                                : "fill-[#D08080]/20 stroke-[#D08080] stroke-2 hover:fill-[#D08080]/40"
                          }`}
                        />
                        {soldOut && center && (
                          <text
                            x={center.x}
                            y={center.y}
                            textAnchor="middle"
                            dominantBaseline="middle"
                            fontSize={soldOutLabelSize}
                            fontWeight={800}
                            fill="#E5E7EB"
                            stroke="#111827"
                            strokeWidth={soldOutLabelSize / 18}
                            paintOrder="stroke"
                            className="pointer-events-none select-none"
                          >
                            SOLD OUT
                          </text>
                        )}
                      </g>
                    );
                  })}
                </svg>
                {zones.length === 0 && (
                  <div className="absolute inset-0 flex items-center justify-center px-6 text-center text-gray-500">
                    Peta area belum tersedia untuk event ini.
                  </div>
                )}
              </div>
            </div>
          </div>

          {/* PANEL SECTION */}
          <div className="w-full md:w-96 flex flex-col gap-4">
            <div className="bg-white/80 backdrop-blur-xl border border-[#E2E8F0] rounded-2xl p-6 shadow-xl h-full">
              {selectedZone ? (
                <div className="animate-in fade-in slide-in-from-bottom-4 duration-300">
                  <span className="text-xs font-bold text-[#D08080] tracking-wider uppercase">Selected Zone</span>
                  <h2 className="text-3xl font-extrabold mt-1 mb-2 text-[#0B0F19]">{selectedZone.name}</h2>
                  <p className="text-2xl font-bold text-[#0B0F19] mb-6">Rp {formatPrice(selectedZone.price)}</p>
                  <div className="space-y-4 mb-6">
                    {selectedZone.pov_image_url && (
                      <div>
                        <p className="text-xs font-semibold uppercase tracking-wider text-slate-500 mb-2">
                          Pemandangan dari Zona
                        </p>
                        <div className="relative h-32 overflow-hidden rounded-lg border border-[#E2E8F0]">
                          <Image
                            src={selectedZone.pov_image_url}
                            alt="POV"
                            fill
                            unoptimized
                            sizes="384px"
                            className="object-cover"
                          />
                        </div>
                      </div>
                    )}
                    <div className="flex justify-between text-sm text-slate-600 border-b border-[#E2E8F0] pb-2">
                      <span>Jarak ke Panggung</span>
                      <span className="font-bold text-[#0B0F19]">{selectedZone.distance || "-"}</span>
                    </div>
                    <div className="flex justify-between text-sm text-slate-600">
                      <span>Sisa Kuota</span>
                      <span className={`font-bold ${selectedSoldOut ? "text-gray-500" : "text-green-600"}`}>
                        {selectedSoldOut ? "Habis" : `${selectedQuota} Tiket`}
                      </span>
                    </div>
                  </div>
                  <button
                    type="button"
                    onClick={handleBuyTicket}
                    disabled={isBuying || selectedSoldOut}
                    aria-disabled={isBuying || selectedSoldOut}
                    className={`w-full font-bold py-3 px-4 rounded-xl transition-colors shadow-lg ${
                      isBuying || selectedSoldOut
                        ? "bg-gray-300 text-gray-600 cursor-not-allowed shadow-none"
                        : "bg-pink-600 hover:bg-pink-500 text-white shadow-pink-900/50"
                    }`}
                  >
                    {isBuying
                      ? "Memproses..."
                      : selectedSoldOut
                        ? "Kuota Habis"
                        : "Beli Tiket Zona Ini"}
                  </button>
                  {errorMsg && (
                    <p className="mt-3 text-sm text-red-400" role="alert">
                      {errorMsg}
                    </p>
                  )}
                </div>
              ) : (
                <div className="h-full flex flex-col items-center justify-center text-slate-500 py-20 text-center">
                  <p>Klik area berwarna di peta untuk melihat detail.</p>
                </div>
              )}
            </div>
          </div>

          {/* MOBILE LIST SECTION */}
          <div className="md:hidden w-full space-y-4">
            {zones.length === 0 ? (
              <div className="bg-zinc-900 border border-zinc-800 rounded-xl p-4 text-zinc-400 text-center">
                Peta area belum tersedia untuk event ini.
              </div>
            ) : (
              zones.map((zone) => {
                const soldOut = isZoneSoldOut(zone);
                return (
                  <div
                    key={zone.id}
                    className={`bg-zinc-900 border border-zinc-800 rounded-xl p-4 ${
                      soldOut ? "opacity-60" : "cursor-pointer"
                    }`}
                    onClick={() => selectZone(zone)}
                  >
                    <h3 className="font-bold text-lg">{zone.name}</h3>
                    <p className="text-zinc-400 text-sm">{zone.distance || "-"} dari panggung</p>
                    <p className="text-pink-500 font-bold mt-2">Rp {formatPrice(zone.price)}</p>
                    {soldOut && <p className="text-gray-400 text-xs font-bold mt-1">SOLD OUT</p>}
                  </div>
                );
              })
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
