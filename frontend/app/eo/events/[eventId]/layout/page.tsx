type ZoneData = {
  id: string;
  name: string;
  price: number;
  quota: number;
  polygon_points: string;
  pov_image_url: string;
  distance: string;
};
'use client';

import { useState } from 'react';
import { useParams } from 'next/navigation';

export default function EOLayoutPage() {
  const { eventId } = useParams();
  const [imageUrl, setImageUrl] = useState('');
  const [width, setWidth] = useState(800);
  const [height, setHeight] = useState(600);
  const [mode, setMode] = useState('ZONED');
  const [zonesJson, setZonesJson] = useState('[]');
  const [saving, setSaving] = useState(false);

  let zones: ZoneData[] = [];
  try {
    zones = JSON.parse(zonesJson);
  } catch {
    zones = [];
  }

  async function handleSave() {
    setSaving(true);
    try {
      await fetch(`/eo/events/${eventId}/layout`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          layout_image_url: imageUrl,
          layout_image_width: width,
          layout_image_height: height,
          seating_mode: mode,
          layout_zones: zonesJson,
        }),
      });
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="min-h-screen bg-[#0B0F19] text-white p-6">
      <h1 className="text-2xl font-bold mb-6">EO Layout Editor - {eventId}</h1>
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6 max-w-7xl mx-auto">
        <div className="space-y-4 bg-zinc-900 p-6 rounded-2xl border border-zinc-800">
          <div>
            <label className="block text-sm mb-1">Image URL</label>
            <input className="w-full bg-zinc-800 rounded p-2" value={imageUrl} onChange={(e) => setImageUrl(e.target.value)} />
          </div>
          <div className="flex gap-4">
            <div className="flex-1">
              <label className="block text-sm mb-1">Width (px)</label>
              <input type="number" className="w-full bg-zinc-800 rounded p-2" value={width} onChange={(e) => setWidth(Number(e.target.value))} />
            </div>
            <div className="flex-1">
              <label className="block text-sm mb-1">Height (px)</label>
              <input type="number" className="w-full bg-zinc-800 rounded p-2" value={height} onChange={(e) => setHeight(Number(e.target.value))} />
            </div>
          </div>
          <div>
            <label className="block text-sm mb-1">Seating Mode</label>
            <select className="w-full bg-zinc-800 rounded p-2" value={mode} onChange={(e) => setMode(e.target.value)}>
              <option value="ZONED">ZONED</option>
              <option value="ASSIGNED">ASSIGNED</option>
            </select>
          </div>
          <div>
            <label className="block text-sm mb-1">Layout Zones JSON</label>
            <textarea className="w-full bg-zinc-800 rounded p-2 h-40" value={zonesJson} onChange={(e) => setZonesJson(e.target.value)} />
          </div>
          <button onClick={handleSave} disabled={saving} className="bg-pink-600 hover:bg-pink-500 px-4 py-2 rounded-xl font-bold">
            {saving ? 'Menyimpan...' : 'Simpan Layout'}
          </button>
        </div>
        <div className="bg-zinc-900 p-6 rounded-2xl border border-zinc-800">
          <h2 className="text-lg font-bold mb-4">Live Preview</h2>
          <div className="relative">
            {imageUrl ? (
              <img src={imageUrl} alt="preview" className="w-full h-auto opacity-70" />
            ) : (
              <div className="w-full h-64 bg-zinc-800 flex items-center justify-center">No image</div>
            )}
            <svg viewBox={`0 0 ${width} ${height}`} className="absolute inset-0 w-full h-full" preserveAspectRatio="xMidYMid meet">
              {zones.map((z: ZoneData, i: number) => (
                <polygon key={i} points={z.polygon_points} className="fill-pink-500/40 stroke-pink-400" />
              ))}
            </svg>
          </div>
        </div>
      </div>
    </div>
  );
}
