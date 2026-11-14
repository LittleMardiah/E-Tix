"use client";

import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type PointerEvent as ReactPointerEvent,
} from "react";
import { useParams } from "next/navigation";
import { apiClient, getApiErrorMessage } from "@/lib/api/client";

type ZoneType = "STANDING" | "SEATED";

type ZoneData = {
  id: string;
  name: string;
  price: number;
  quota: number;
  type?: ZoneType;
  color?: string;
  polygon_points: string;
  pov_image_url: string;
  distance: string;
};

type Point = { x: number; y: number };
type Pan = { x: number; y: number };

type ZoneForm = {
  name: string;
  price: string;
  quota: string;
  type: ZoneType;
  color: string;
};

type ToastState = { kind: "success" | "error" | "info"; text: string } | null;

type LayoutDraft = {
  imageUrl: string;
  width: number;
  height: number;
  mode: string;
  zonesJson: string;
  savedAt: string;
};

const DRAFT_KEY_PREFIX = "e-tix-flash:eo-layout-draft:";
const DRAG_THRESHOLD = 4;
const MIN_ZOOM = 1;
const MAX_ZOOM = 6;
const ZOOM_STEP = 1.25;
const DEFAULT_ZONE_COLOR = "#D08080";

const EMPTY_FORM: ZoneForm = {
  name: "",
  price: "",
  quota: "",
  type: "STANDING",
  color: DEFAULT_ZONE_COLOR,
};

function parseZones(raw: string): ZoneData[] | null {
  try {
    const parsed = JSON.parse(raw);
    return Array.isArray(parsed) ? (parsed as ZoneData[]) : null;
  } catch {
    return null;
  }
}

function formatPoints(points: Point[]): string {
  return points
    .map((point) => `${Math.round(point.x)},${Math.round(point.y)}`)
    .join(" ");
}

function clampZoom(value: number): number {
  return Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, value));
}

export default function EOLayoutPage() {
  const params = useParams<{ eventId: string }>();
  const eventId = typeof params?.eventId === "string" ? params.eventId : "";

  const [imageUrl, setImageUrl] = useState("");
  const [width, setWidth] = useState(800);
  const [height, setHeight] = useState(600);
  const [dimsTouched, setDimsTouched] = useState(false);
  const [mode, setMode] = useState("ZONED");
  const [opacity, setOpacity] = useState(1);
  const [zonesJson, setZonesJson] = useState("[]");
  const [zonesList, setZonesList] = useState<ZoneData[]>([]);
  const [saving, setSaving] = useState(false);

  const [zoom, setZoom] = useState(1);
  const [pan, setPan] = useState<Pan>({ x: 0, y: 0 });
  const [points, setPoints] = useState<Point[]>([]);

  const [modalOpen, setModalOpen] = useState(false);
  const [editingIndex, setEditingIndex] = useState<number | null>(null);
  const [form, setForm] = useState<ZoneForm>(EMPTY_FORM);

  const [toast, setToast] = useState<ToastState>(null);
  const [draftFound, setDraftFound] = useState<LayoutDraft | null>(null);

  const containerRef = useRef<HTMLDivElement>(null);
  const imageRef = useRef<HTMLImageElement>(null);
  const dragRef = useRef<{
    startX: number;
    startY: number;
    panX: number;
    panY: number;
    moved: boolean;
  } | null>(null);

  const isDrawing = points.length > 0;

  useEffect(() => {
    if (!toast) return;
    const timer = setTimeout(() => setToast(null), 4000);
    return () => clearTimeout(timer);
  }, [toast]);

  // Draft discovery. Reading localStorage in an effect keeps SSR output equal
  // to the first client render (no hydration mismatch).
  useEffect(() => {
    if (!eventId) return;
    try {
      const raw = window.localStorage.getItem(DRAFT_KEY_PREFIX + eventId);
      if (!raw) return;
      const parsed = JSON.parse(raw) as LayoutDraft;
      if (parsed && typeof parsed.zonesJson === "string") {
        setDraftFound(parsed);
      }
    } catch {
      // Corrupt draft is ignored; it must never block the editor.
    }
  }, [eventId]);

  // Auto-save the draft every 5s. The ref holds the newest values so the
  // interval is created once and never captures stale state; cleanup clears it.
  const draftRef = useRef({ imageUrl, width, height, mode, zonesJson });
  draftRef.current = { imageUrl, width, height, mode, zonesJson };

  useEffect(() => {
    if (!eventId) return;
    const timer = setInterval(() => {
      try {
        const payload: LayoutDraft = {
          ...draftRef.current,
          savedAt: new Date().toISOString(),
        };
        window.localStorage.setItem(
          DRAFT_KEY_PREFIX + eventId,
          JSON.stringify(payload)
        );
      } catch {
        // Quota/privacy errors must not interrupt tracing.
      }
    }, 5000);
    return () => clearInterval(timer);
  }, [eventId]);

  // Keyboard shortcuts: Ctrl/Cmd+Z undoes the last vertex, Esc closes the
  // modal or resets the viewport. Listener is removed on unmount.
  useEffect(() => {
    function onKeyDown(event: KeyboardEvent) {
      const target = event.target as HTMLElement | null;
      if (
        target &&
        (target.tagName === "INPUT" ||
          target.tagName === "TEXTAREA" ||
          target.tagName === "SELECT")
      ) {
        return;
      }
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "z") {
        event.preventDefault();
        setPoints((current) => current.slice(0, -1));
        return;
      }
      if (event.key === "Escape") {
        if (modalOpen) {
          setModalOpen(false);
          setEditingIndex(null);
          return;
        }
        setPoints([]);
        setZoom(1);
        setPan({ x: 0, y: 0 });
      }
    }
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [modalOpen]);

  const applyZones = useCallback((next: ZoneData[]) => {
    setZonesList(next);
    setZonesJson(JSON.stringify(next, null, 2));
  }, []);

  const handleSave = async () => {
    if (parseZones(zonesJson) === null) {
      setToast({ kind: "error", text: "Layout Zones JSON tidak valid." });
      return;
    }
    setSaving(true);
    try {
      await apiClient.put(`/eo/events/${eventId}/layout`, {
        layout_image_url: imageUrl,
        layout_image_width: width,
        layout_image_height: height,
        seating_mode: mode,
        layout_zones: zonesJson,
      });
      setToast({ kind: "success", text: "Layout berhasil disimpan!" });
    } catch (error) {
      console.error("Gagal menyimpan layout:", error);
      setToast({
        kind: "error",
        text: getApiErrorMessage(error, "Gagal menyimpan layout."),
      });
    } finally {
      setSaving(false);
    }
  };

  /**
   * Screen -> logical image coordinate.
   * `pan` is applied before `scale`, so it is in screen pixels and `zoom`
   * divides only the layout offset. offsetWidth/offsetHeight are layout
   * metrics: CSS transform does NOT change them, which is exactly why they
   * are used here instead of getBoundingClientRect().
   */
  const screenToImage = useCallback(
    (clientX: number, clientY: number): Point | null => {
      const container = containerRef.current;
      const image = imageRef.current;
      if (!container || !image || image.offsetWidth === 0) return null;

      const rect = container.getBoundingClientRect();
      const localX = (clientX - rect.left - pan.x) / zoom;
      const localY = (clientY - rect.top - pan.y) / zoom;
      if (
        localX < 0 ||
        localY < 0 ||
        localX > image.offsetWidth ||
        localY > image.offsetHeight
      ) {
        return null;
      }
      return {
        x: (localX / image.offsetWidth) * width,
        y: (localY / image.offsetHeight) * height,
      };
    },
    [pan.x, pan.y, zoom, width, height]
  );

  const handlePointerDown = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (event.button !== 0) return;
    dragRef.current = {
      startX: event.clientX,
      startY: event.clientY,
      panX: pan.x,
      panY: pan.y,
      moved: false,
    };
  };

  const handlePointerMove = (event: ReactPointerEvent<HTMLDivElement>) => {
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
    }
    if (drag.moved) {
      setPan({ x: drag.panX + dx, y: drag.panY + dy });
    }
  };

  const handlePointerUp = (event: ReactPointerEvent<HTMLDivElement>) => {
    const drag = dragRef.current;
    dragRef.current = null;
    if (!drag || drag.moved) return;
    const point = screenToImage(event.clientX, event.clientY);
    if (!point) return;
    setPoints((current) => [...current, point]);
  };

  const resetView = () => {
    setZoom(1);
    setPan({ x: 0, y: 0 });
  };

  const zoomBy = (factor: number) => {
    setZoom((current) => clampZoom(current * factor));
  };

  const handleFinishArea = () => {
    if (points.length < 3) {
      setToast({
        kind: "error",
        text: "Minimal 3 titik untuk membentuk area.",
      });
      return;
    }
    setEditingIndex(null);
    setForm(EMPTY_FORM);
    setModalOpen(true);
  };

  const openEditZone = (index: number) => {
    const zone = zonesList[index];
    if (!zone) return;
    setEditingIndex(index);
    setForm({
      name: zone.name ?? "",
      price: String(zone.price ?? ""),
      quota: String(zone.quota ?? ""),
      type: zone.type ?? "STANDING",
      color: zone.color ?? DEFAULT_ZONE_COLOR,
    });
    setModalOpen(true);
  };

  const closeModal = () => {
    setModalOpen(false);
    setEditingIndex(null);
  };

  const submitZone = () => {
    const name = form.name.trim();
    const price = Number(form.price);
    const quota = Number(form.quota);
    if (!name) {
      setToast({ kind: "error", text: "Nama zona wajib diisi." });
      return;
    }
    if (!Number.isFinite(price) || price < 0) {
      setToast({ kind: "error", text: "Harga harus angka >= 0." });
      return;
    }
    if (!Number.isInteger(quota) || quota < 0) {
      setToast({ kind: "error", text: "Kuota harus bilangan bulat >= 0." });
      return;
    }

    if (editingIndex === null) {
      const zone: ZoneData = {
        id: `z-${Date.now()}-${Math.round(Math.random() * 1e6)}`,
        name,
        price,
        quota,
        type: form.type,
        color: form.color,
        polygon_points: formatPoints(points),
        pov_image_url: "",
        distance: "",
      };
      applyZones([...zonesList, zone]);
      setPoints([]);
      setToast({ kind: "success", text: `Zona ${name} ditambahkan.` });
    } else {
      const next = zonesList.map((zone, index) =>
        index === editingIndex
          ? {
              ...zone,
              name,
              price,
              quota,
              type: form.type,
              color: form.color,
            }
          : zone
      );
      applyZones(next);
      setToast({ kind: "success", text: `Zona ${name} diperbarui.` });
    }
    closeModal();
  };

  const deleteZone = () => {
    if (editingIndex === null) return;
    const next = zonesList.filter((_, index) => index !== editingIndex);
    applyZones(next);
    closeModal();
    setToast({ kind: "info", text: "Zona dihapus." });
  };

  const restoreDraft = () => {
    if (!draftFound) return;
    setImageUrl(draftFound.imageUrl);
    setWidth(draftFound.width);
    setHeight(draftFound.height);
    setDimsTouched(true);
    setMode(draftFound.mode);
    setZonesJson(draftFound.zonesJson);
    setZonesList(parseZones(draftFound.zonesJson) ?? []);
    setDraftFound(null);
    setToast({ kind: "info", text: "Draft berhasil dipulihkan." });
  };

  const discardDraft = () => {
    if (eventId) {
      try {
        window.localStorage.removeItem(DRAFT_KEY_PREFIX + eventId);
      } catch {
        // Ignore storage errors.
      }
    }
    setDraftFound(null);
  };

  const handleZonesTextChange = (raw: string) => {
    setZonesJson(raw);
    const parsed = parseZones(raw);
    if (parsed) setZonesList(parsed);
  };

  const handleImageLoad = () => {
    const image = imageRef.current;
    if (!image || dimsTouched) return;
    if (width === 800 && height === 600 && image.naturalWidth > 0) {
      setWidth(image.naturalWidth);
      setHeight(image.naturalHeight);
    }
  };

  const draftPolygon = formatPoints(points);

  return (
    <div className="min-h-screen bg-[#0B0F19] text-white p-6">
      <div className="max-w-7xl mx-auto">
        <div className="flex flex-wrap items-center justify-between gap-4 mb-6">
          <div>
            <h1 className="text-2xl font-bold">EO Layout Editor</h1>
            <p className="text-sm text-zinc-400">{eventId}</p>
          </div>
          <div className="flex flex-wrap gap-2">
            <button
              type="button"
              onClick={() =>
                window.open(`/customer/map/${eventId}`, "_blank", "noopener,noreferrer")
              }
              className="rounded-lg border border-zinc-700 px-4 py-2 text-sm font-medium hover:border-pink-500"
            >
              Preview Customer Map
            </button>
            <button
              type="button"
              onClick={() => void handleSave()}
              disabled={saving}
              className="rounded-lg bg-pink-600 px-4 py-2 text-sm font-bold hover:bg-pink-500 disabled:opacity-50"
            >
              {saving ? "Menyimpan..." : "Simpan Layout"}
            </button>
          </div>
        </div>

        {draftFound && (
          <div
            role="status"
            className="mb-4 flex flex-wrap items-center justify-between gap-3 rounded-xl border border-amber-500/40 bg-amber-500/10 px-4 py-3 text-sm text-amber-200"
          >
            <span>
              Draft tersimpan ditemukan ({new Date(draftFound.savedAt).toLocaleString("id-ID")}).
            </span>
            <span className="flex gap-2">
              <button
                type="button"
                onClick={restoreDraft}
                className="rounded-md bg-amber-400 px-3 py-1 font-semibold text-amber-950"
              >
                Pulihkan
              </button>
              <button
                type="button"
                onClick={discardDraft}
                className="rounded-md border border-amber-400/60 px-3 py-1 font-semibold"
              >
                Abaikan
              </button>
            </span>
          </div>
        )}

        <div className="grid grid-cols-1 gap-6 xl:grid-cols-3">
          <div className="space-y-4 rounded-2xl border border-zinc-800 bg-zinc-900 p-6">
            <div>
              <label className="mb-1 block text-sm" htmlFor="layout-image-url">
                Image URL
              </label>
              <input
                id="layout-image-url"
                className="w-full rounded bg-zinc-800 p-2"
                value={imageUrl}
                onChange={(event) => setImageUrl(event.target.value)}
                placeholder="/images/map-blackpink.jpg"
              />
            </div>
            <div className="flex gap-4">
              <div className="flex-1">
                <label className="mb-1 block text-sm" htmlFor="layout-width">
                  Width (px)
                </label>
                <input
                  id="layout-width"
                  type="number"
                  min={1}
                  className="w-full rounded bg-zinc-800 p-2"
                  value={width}
                  onChange={(event) => {
                    setDimsTouched(true);
                    setWidth(Number(event.target.value));
                  }}
                />
              </div>
              <div className="flex-1">
                <label className="mb-1 block text-sm" htmlFor="layout-height">
                  Height (px)
                </label>
                <input
                  id="layout-height"
                  type="number"
                  min={1}
                  className="w-full rounded bg-zinc-800 p-2"
                  value={height}
                  onChange={(event) => {
                    setDimsTouched(true);
                    setHeight(Number(event.target.value));
                  }}
                />
              </div>
            </div>
            <div>
              <label className="mb-1 block text-sm" htmlFor="seating-mode">
                Seating Mode
              </label>
              <select
                id="seating-mode"
                className="w-full rounded bg-zinc-800 p-2"
                value={mode}
                onChange={(event) => setMode(event.target.value)}
              >
                <option value="ZONED">ZONED</option>
                <option value="ASSIGNED">ASSIGNED</option>
              </select>
            </div>
            <div>
              <label className="mb-1 block text-sm" htmlFor="image-opacity">
                Opacity Gambar: {Math.round(opacity * 100)}%
              </label>
              <input
                id="image-opacity"
                type="range"
                min={0.2}
                max={1}
                step={0.05}
                value={opacity}
                onChange={(event) => setOpacity(Number(event.target.value))}
                className="w-full accent-pink-500"
              />
            </div>
            <div>
              <label className="mb-1 block text-sm" htmlFor="zones-json">
                Layout Zones JSON
              </label>
              <textarea
                id="zones-json"
                className="h-40 w-full rounded bg-zinc-800 p-2 font-mono text-xs"
                value={zonesJson}
                onChange={(event) => handleZonesTextChange(event.target.value)}
              />
            </div>
            <div className="rounded-xl border border-zinc-800 bg-zinc-950/60 p-3 text-xs text-zinc-400">
              <p className="font-semibold text-zinc-300">Shortcut</p>
              <p>Ctrl/Cmd + Z = undo titik terakhir</p>
              <p>Esc = tutup modal / reset view</p>
              <p>Drag = pan, klik = tambah titik</p>
            </div>
          </div>

          <div className="xl:col-span-2">
            <div className="mb-3 flex flex-wrap items-center gap-2">
              <button
                type="button"
                onClick={() => zoomBy(ZOOM_STEP)}
                disabled={zoom >= MAX_ZOOM}
                className="h-9 w-9 rounded-lg border border-zinc-700 text-lg font-bold hover:border-pink-500 disabled:opacity-40"
                aria-label="Zoom in"
              >
                +
              </button>
              <button
                type="button"
                onClick={() => zoomBy(1 / ZOOM_STEP)}
                disabled={zoom <= MIN_ZOOM}
                className="h-9 w-9 rounded-lg border border-zinc-700 text-lg font-bold hover:border-pink-500 disabled:opacity-40"
                aria-label="Zoom out"
              >
                -
              </button>
              <button
                type="button"
                onClick={resetView}
                className="h-9 rounded-lg border border-zinc-700 px-3 text-sm hover:border-pink-500"
              >
                Reset View
              </button>
              <span className="text-xs text-zinc-400">
                Zoom {Math.round(zoom * 100)}% &bull; {points.length} titik
              </span>
              <div className="ml-auto flex flex-wrap gap-2">
                <button
                  type="button"
                  onClick={() => setPoints((current) => current.slice(0, -1))}
                  disabled={!isDrawing}
                  className="h-9 rounded-lg border border-zinc-700 px-3 text-sm hover:border-pink-500 disabled:opacity-40"
                >
                  Undo Titik Terakhir
                </button>
                <button
                  type="button"
                  onClick={handleFinishArea}
                  disabled={points.length < 3}
                  className="h-9 rounded-lg bg-pink-600 px-3 text-sm font-bold hover:bg-pink-500 disabled:opacity-40"
                >
                  Selesaikan Area &amp; Simpan
                </button>
              </div>
            </div>

            <div
              ref={containerRef}
              onPointerDown={handlePointerDown}
              onPointerMove={handlePointerMove}
              onPointerUp={handlePointerUp}
              onPointerCancel={() => {
                dragRef.current = null;
              }}
              className={`relative h-[70vh] min-h-[420px] touch-none select-none overflow-hidden rounded-2xl border border-zinc-700 bg-zinc-950 ${
                isDrawing ? "cursor-crosshair" : "cursor-grab"
              }`}
            >
              {imageUrl ? (
                <div
                  className="absolute left-0 top-0 w-full"
                  style={{
                    transform: `translate(${pan.x}px, ${pan.y}px) scale(${zoom})`,
                    transformOrigin: "0 0",
                  }}
                >
                  {/* eslint-disable-next-line @next/next/no-img-element */}
                  <img
                    ref={imageRef}
                    src={imageUrl}
                    alt="Layout preview"
                    onLoad={handleImageLoad}
                    draggable={false}
                    style={{ opacity }}
                    className="block h-auto w-full select-none"
                  />
                  <svg
                    viewBox={`0 0 ${width} ${height}`}
                    preserveAspectRatio="none"
                    className="pointer-events-none absolute inset-0 h-full w-full"
                  >
                    {zonesList.map((zone, index) => (
                      <polygon
                        key={zone.id || index}
                        points={zone.polygon_points}
                        onPointerDown={(event) => event.stopPropagation()}
                        onClick={(event) => {
                          event.stopPropagation();
                          openEditZone(index);
                        }}
                        style={{ cursor: "pointer" }}
                        fill={zone.color ?? DEFAULT_ZONE_COLOR}
                        fillOpacity={0.35}
                        stroke={zone.color ?? DEFAULT_ZONE_COLOR}
                        strokeWidth={2}
                        className="pointer-events-auto drop-shadow-[0_0_8px_rgba(208,128,128,0.6)]"
                      />
                    ))}
                    {points.length > 0 && (
                      <polygon
                        points={draftPolygon}
                        fill="rgba(208,128,128,0.25)"
                        stroke="#F9A8D4"
                        strokeWidth={2}
                        strokeDasharray="6 4"
                      />
                    )}
                    {points.map((point, index) => (
                      <circle
                        key={index}
                        cx={point.x}
                        cy={point.y}
                        r={Math.max(4, width / 200)}
                        fill="#F9A8D4"
                        stroke="#0B0F19"
                        strokeWidth={1}
                      />
                    ))}
                  </svg>
                </div>
              ) : (
                <div className="flex h-full w-full items-center justify-center text-zinc-500">
                  Isi Image URL dulu untuk mulai tracing.
                </div>
              )}
            </div>

            <div className="mt-4 rounded-2xl border border-zinc-800 bg-zinc-900 p-4">
              <div className="mb-2 flex items-center justify-between">
                <h2 className="text-sm font-bold">Zona ({zonesList.length})</h2>
                <button
                  type="button"
                  onClick={() => setPoints([])}
                  disabled={!isDrawing}
                  className="text-xs text-zinc-400 hover:text-pink-400 disabled:opacity-40"
                >
                  Batalkan tracing
                </button>
              </div>
              {zonesList.length === 0 ? (
                <p className="text-xs text-zinc-500">
                  Belum ada zona. Klik gambar minimal 3 titik lalu
                  &quot;Selesaikan Area&quot;.
                </p>
              ) : (
                <ul className="space-y-1 text-sm">
                  {zonesList.map((zone, index) => (
                    <li
                      key={zone.id || index}
                      className="flex items-center justify-between gap-3 rounded-lg px-2 py-1 hover:bg-zinc-800"
                    >
                      <span className="flex items-center gap-2">
                        <span
                          aria-hidden
                          className="inline-block h-3 w-3 rounded-full"
                          style={{ backgroundColor: zone.color ?? DEFAULT_ZONE_COLOR }}
                        />
                        {zone.name}
                        <span className="text-xs text-zinc-500">
                          Rp {zone.price} &bull; {zone.quota} tiket
                        </span>
                      </span>
                      <button
                        type="button"
                        onClick={() => openEditZone(index)}
                        className="text-xs text-pink-400 hover:underline"
                      >
                        Edit
                      </button>
                    </li>
                  ))}
                </ul>
              )}
            </div>
          </div>
        </div>
      </div>

      {modalOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 p-4">
          <div
            role="dialog"
            aria-modal="true"
            aria-labelledby="zone-modal-title"
            className="w-full max-w-md rounded-2xl border border-zinc-700 bg-zinc-900 p-6 shadow-2xl"
          >
            <h2 id="zone-modal-title" className="mb-4 text-lg font-bold">
              {editingIndex === null ? "Simpan Area Baru" : "Edit Zona"}
            </h2>
            <div className="space-y-3">
              <div>
                <label className="mb-1 block text-sm" htmlFor="zone-name">
                  Nama Zona
                </label>
                <input
                  id="zone-name"
                  value={form.name}
                  onChange={(event) =>
                    setForm((current) => ({ ...current, name: event.target.value }))
                  }
                  className="w-full rounded bg-zinc-800 p-2"
                  placeholder="VIP"
                  autoFocus
                />
              </div>
              <div className="flex gap-3">
                <div className="flex-1">
                  <label className="mb-1 block text-sm" htmlFor="zone-price">
                    Harga (Rp)
                  </label>
                  <input
                    id="zone-price"
                    type="number"
                    min={0}
                    value={form.price}
                    onChange={(event) =>
                      setForm((current) => ({ ...current, price: event.target.value }))
                    }
                    className="w-full rounded bg-zinc-800 p-2"
                  />
                </div>
                <div className="flex-1">
                  <label className="mb-1 block text-sm" htmlFor="zone-quota">
                    Kuota
                  </label>
                  <input
                    id="zone-quota"
                    type="number"
                    min={0}
                    value={form.quota}
                    onChange={(event) =>
                      setForm((current) => ({ ...current, quota: event.target.value }))
                    }
                    className="w-full rounded bg-zinc-800 p-2"
                  />
                </div>
              </div>
              <div className="flex gap-3">
                <div className="flex-1">
                  <label className="mb-1 block text-sm" htmlFor="zone-type">
                    Tipe
                  </label>
                  <select
                    id="zone-type"
                    value={form.type}
                    onChange={(event) =>
                      setForm((current) => ({
                        ...current,
                        type: event.target.value as ZoneType,
                      }))
                    }
                    className="w-full rounded bg-zinc-800 p-2"
                  >
                    <option value="STANDING">STANDING</option>
                    <option value="SEATED">SEATED</option>
                  </select>
                </div>
                <div className="flex-1">
                  <label className="mb-1 block text-sm" htmlFor="zone-color">
                    Warna
                  </label>
                  <input
                    id="zone-color"
                    type="color"
                    value={form.color}
                    onChange={(event) =>
                      setForm((current) => ({ ...current, color: event.target.value }))
                    }
                    className="h-10 w-full rounded bg-zinc-800 p-1"
                  />
                </div>
              </div>
              {editingIndex === null && (
                <p className="text-xs text-zinc-400">
                  Titik tersimpan: {points.length}
                </p>
              )}
            </div>
            <div className="mt-6 flex flex-wrap justify-end gap-2">
              {editingIndex !== null && (
                <button
                  type="button"
                  onClick={deleteZone}
                  className="mr-auto rounded-lg border border-rose-500/60 px-3 py-2 text-sm font-medium text-rose-300 hover:bg-rose-500/10"
                >
                  Hapus Zona
                </button>
              )}
              <button
                type="button"
                onClick={closeModal}
                className="rounded-lg border border-zinc-700 px-3 py-2 text-sm"
              >
                Batal
              </button>
              <button
                type="button"
                onClick={submitZone}
                className="rounded-lg bg-pink-600 px-4 py-2 text-sm font-bold hover:bg-pink-500"
              >
                Simpan Zona
              </button>
            </div>
          </div>
        </div>
      )}

      {toast && (
        <div
          role="status"
          className={`fixed bottom-4 right-4 z-[60] max-w-sm rounded-xl border px-4 py-3 text-sm shadow-xl ${
            toast.kind === "success"
              ? "border-green-500/40 bg-green-500/15 text-green-200"
              : toast.kind === "error"
                ? "border-rose-500/40 bg-rose-500/15 text-rose-200"
                : "border-zinc-600 bg-zinc-800 text-zinc-200"
          }`}
        >
          {toast.text}
        </div>
      )}
    </div>
  );
}
