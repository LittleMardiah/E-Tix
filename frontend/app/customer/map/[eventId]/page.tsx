'use client';

import { useEffect, useState } from 'react';
import { useParams } from 'next/navigation';
import { apiClient, getApiErrorMessage } from '@/lib/api/client';

type ZoneData = {
  id: string;
  name: string;
  price: number | string;
  quota?: number;
  polygon_points: string;
  pov_image_url?: string;
  distance?: string;
};

type EventDetailData = {
  name?: string;
  layout_image_url?: string;
  layout_image_width?: number;
  layout_image_height?: number;
  layout_zones?: ZoneData[] | string | null;
};

function formatPrice(value: number | string | undefined): string {
  const numeric = typeof value === 'number' ? value : Number(value ?? 0);
  if (Number.isNaN(numeric)) {
    return String(value ?? '0');
  }
  return numeric.toLocaleString('id-ID');
}

function parseZones(value: EventDetailData['layout_zones']): ZoneData[] {
  if (!value) return [];
  const parsed = typeof value === 'string' ? JSON.parse(value) : value;
  return Array.isArray(parsed) ? parsed : [];
}

export default function InteractiveMap() {
  const params = useParams();
  const eventId = Array.isArray(params?.eventId) ? params.eventId[0] : params?.eventId;
  const [selectedZone, setSelectedZone] = useState<ZoneData | null>(null);
  const [zones, setZones] = useState<ZoneData[]>([]);
  const [eventData, setEventData] = useState<EventDetailData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!eventId) return;

    let cancelled = false;

    async function fetchData() {
      setLoading(true);
      setError(null);
      try {
        const res = await apiClient.get(`/events/${eventId}`);
        if (cancelled) return;

        const data: EventDetailData | null = res.data?.data ?? null;
        setEventData(data);
        setZones(parseZones(data?.layout_zones));
      } catch (err) {
        if (cancelled) return;
        console.error('Gagal load map', err);
        setError(getApiErrorMessage(err, 'Gagal memuat data peta.'));
        setZones([]);
      } finally {
        if (!cancelled) setLoading(false);
      }
    }

    fetchData();

    return () => {
      cancelled = true;
    };
  }, [eventId]);

  const viewBoxWidth = eventData?.layout_image_width || 800;
  const viewBoxHeight = eventData?.layout_image_height || 600;
  const mapImage = eventData?.layout_image_url || 'https://placehold.co/800x600/202020/FFFFFF?text=Venue+Map+(Placeholder)';

  if (loading) {
    return (
      <div className="min-h-screen bg-[#0B0F19] text-white flex items-center justify-center p-8">
        <p className="text-zinc-400">Memuat peta...</p>
      </div>
    );
  }

  return (
    <div className="min-h-screen bg-[#0B0F19] text-white p-4 md:p-8 font-sans">
      <h1 className="text-2xl font-bold mb-6 text-center md:text-left">Pilih Area Konser - {eventId}</h1>

      {error && (
        <div className="max-w-7xl mx-auto mb-4 rounded-xl border border-red-500/40 bg-red-500/10 px-4 py-3 text-sm text-red-300">
          {error}
        </div>
      )}

      <div className="flex flex-col md:flex-row gap-8 max-w-7xl mx-auto">
        {/* MAP SECTION */}
        <div className="hidden md:block flex-1 relative bg-zinc-900 rounded-2xl overflow-hidden border border-zinc-800 shadow-2xl">
          <img
            src={mapImage}
            alt="Venue Map"
            className="w-full h-auto opacity-70"
          />
          <svg viewBox={`0 0 ${viewBoxWidth} ${viewBoxHeight}`} className="absolute inset-0 w-full h-full" preserveAspectRatio="xMidYMid meet">
            {zones.map((zone) => (
              <polygon
                key={zone.id}
                points={zone.polygon_points}
                className={`cursor-pointer transition-all duration-300 ${
                  selectedZone?.id === zone.id
                    ? 'fill-pink-500/60 stroke-pink-400 stroke-2'
                    : 'fill-white/10 stroke-white/30 hover:fill-white/20'
                }`}
                onClick={() => setSelectedZone(zone)}
              />
            ))}
          </svg>
          {zones.length === 0 && (
            <div className="absolute inset-0 flex items-center justify-center text-zinc-400 px-6 text-center">
              Peta area belum tersedia untuk event ini.
            </div>
          )}
        </div>

        {/* PANEL SECTION */}
        <div className="w-full md:w-96 flex flex-col gap-4">
          <div className="bg-zinc-900/80 backdrop-blur-xl border border-zinc-700/50 rounded-2xl p-6 shadow-xl h-full">
            {selectedZone ? (
              <div className="animate-in fade-in slide-in-from-bottom-4 duration-300">
                <span className="text-xs font-bold text-pink-500 tracking-wider uppercase">Selected Zone</span>
                <h2 className="text-3xl font-extrabold mt-1 mb-2">{selectedZone.name}</h2>
                <p className="text-2xl font-bold text-white mb-6">Rp {formatPrice(selectedZone.price)}</p>
                <div className="space-y-4 mb-6">
                  {selectedZone.pov_image_url && (
                    <div className="rounded-lg overflow-hidden border border-zinc-700">
                      <img src={selectedZone.pov_image_url} alt="POV" className="w-full h-32 object-cover" />
                    </div>
                  )}
                  <div className="flex justify-between text-sm text-zinc-300 border-b border-zinc-800 pb-2">
                    <span>Jarak ke Panggung</span>
                    <span className="font-bold text-white">{selectedZone.distance || '-'}</span>
                  </div>
                  <div className="flex justify-between text-sm text-zinc-300">
                    <span>Sisa Kuota</span>
                    <span className="font-bold text-green-400">{selectedZone.quota ?? 0} Tiket</span>
                  </div>
                </div>
                <button className="w-full bg-pink-600 hover:bg-pink-500 text-white font-bold py-3 px-4 rounded-xl transition-colors shadow-lg shadow-pink-900/50">
                  Beli Tiket Zona Ini
                </button>
              </div>
            ) : (
              <div className="h-full flex flex-col items-center justify-center text-zinc-500 py-20 text-center">
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
            zones.map((zone) => (
              <div key={zone.id} className="bg-zinc-900 border border-zinc-800 rounded-xl p-4" onClick={() => setSelectedZone(zone)}>
                <h3 className="font-bold text-lg">{zone.name}</h3>
                <p className="text-zinc-400 text-sm">{zone.distance || '-'} dari panggung</p>
                <p className="text-pink-500 font-bold mt-2">Rp {formatPrice(zone.price)}</p>
              </div>
            ))
          )}
        </div>
      </div>
    </div>
  );
}
