'use client';

import { useState } from 'react';
import { useParams } from 'next/navigation';

const MOCK_ZONES = [
  {
    id: 'zone_vip',
    name: 'VIP (Front Stage)',
    price: 1500000,
    quota: 50,
    polygon_points: '300,100 500,100 520,250 280,250',
    pov_image_url: 'https://placehold.co/400x200/1a1a1a/FFFFFF?text=POV+VIP',
    distance: '10 meter'
  },
  {
    id: 'zone_cat1',
    name: 'CAT 1',
    price: 1000000,
    quota: 200,
    polygon_points: '250,260 550,260 600,400 200,400',
    pov_image_url: 'https://placehold.co/400x200/1a1a1a/FFFFFF?text=POV+CAT+1',
    distance: '25 meter'
  }
];

export default function InteractiveMap() {
  const { eventId } = useParams();
  const [selectedZone, setSelectedZone] = useState<typeof MOCK_ZONES[0] | null>(null);

  return (
    <div className="min-h-screen bg-[#0B0F19] text-white p-4 md:p-8 font-sans">
      <h1 className="text-2xl font-bold mb-6 text-center md:text-left">Pilih Area Konser - {eventId}</h1>
      
      <div className="flex flex-col md:flex-row gap-8 max-w-7xl mx-auto">
        {/* MAP SECTION */}
        <div className="hidden md:block flex-1 relative bg-zinc-900 rounded-2xl overflow-hidden border border-zinc-800 shadow-2xl">
          <img 
            src="https://placehold.co/800x600/202020/FFFFFF?text=Venue+Map" 
            alt="Venue Map" 
            className="w-full h-auto opacity-70"
          />
          <svg viewBox="0 0 800 600" className="absolute inset-0 w-full h-full" preserveAspectRatio="xMidYMid meet">
            {MOCK_ZONES.map((zone) => (
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
        </div>

        {/* PANEL SECTION */}
        <div className="w-full md:w-96 flex flex-col gap-4">
          <div className="bg-zinc-900/80 backdrop-blur-xl border border-zinc-700/50 rounded-2xl p-6 shadow-xl h-full">
            {selectedZone ? (
              <div className="animate-in fade-in slide-in-from-bottom-4 duration-300">
                <span className="text-xs font-bold text-pink-500 tracking-wider uppercase">Selected Zone</span>
                <h2 className="text-3xl font-extrabold mt-1 mb-2">{selectedZone.name}</h2>
                <p className="text-2xl font-bold text-white mb-6">Rp {selectedZone.price.toLocaleString('id-ID')}</p>
                <div className="space-y-4 mb-6">
                  <div className="rounded-lg overflow-hidden border border-zinc-700">
                    <img src={selectedZone.pov_image_url} alt="POV" className="w-full h-32 object-cover" />
                  </div>
                  <div className="flex justify-between text-sm text-zinc-300 border-b border-zinc-800 pb-2">
                    <span>Jarak ke Panggung</span>
                    <span className="font-bold text-white">{selectedZone.distance}</span>
                  </div>
                  <div className="flex justify-between text-sm text-zinc-300">
                    <span>Sisa Kuota</span>
                    <span className="font-bold text-green-400">{selectedZone.quota} Tiket</span>
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
          {MOCK_ZONES.map((zone) => (
            <div key={zone.id} className="bg-zinc-900 border border-zinc-800 rounded-xl p-4" onClick={() => setSelectedZone(zone)}>
              <h3 className="font-bold text-lg">{zone.name}</h3>
              <p className="text-zinc-400 text-sm">{zone.distance} dari panggung</p>
              <p className="text-pink-500 font-bold mt-2">Rp {zone.price.toLocaleString('id-ID')}</p>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
