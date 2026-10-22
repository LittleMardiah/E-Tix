import Link from 'next/link';
import { Button } from '@/components/ui/Button';
import { Card } from '@/components/ui/Card';

const features = [
  {
    title: 'Flash Sale Ready',
    desc: 'Anti-bot ingress dengan virtual waiting room, menahan lonjakan tens of thousands RPS tanpa oversell.',
  },
  {
    title: 'Atomic Group Locking',
    desc: 'Redis bitmask locking menjamin kursi grup berdampingan terkunci secara atomik.',
  },
  {
    title: 'Split-Pay & Resale Terkendali',
    desc: 'Bayar sendiri-sendiri per anggota grup, plus resale dengan batas harga dan fee transparan.',
  },
  {
    title: 'Email OTP Verification',
    desc: 'Verifikasi akun berbasis kode OTP sekali pakai, berl protections terhadap brute force.',
  },
];

const pricingTiers = [
  {
    name: 'Free',
    fee: '0%',
    price: 'Rp 0',
    desc: 'Untuk event kecil di bawah 500 tiket, fitur dasar.',
    features: ['Sell tiket dasar', 'Denah kursi per section', 'QR dinamis'],
  },
  {
    name: 'Pro',
    fee: '2,5%',
    price: 'Fee per tiket terjual',
    desc: 'Untuk event menengah hingga besar yang butuh performa tinggi.',
    features: [
      'Anti-bot ingress 20.000 RPS',
      'Atomic group locking',
      'Split-pay & bulk assignment',
      'Post-event analytics',
    ],
    highlighted: true,
  },
  {
    name: 'Enterprise',
    fee: 'Custom',
    price: 'Harga khusus',
    desc: 'Infrastruktur dedicated, white-label, dan integrasi custom.',
    features: [
      'Dedicated infrastructure',
      'White-label',
      'Custom integration',
      'Priority support',
    ],
  },
];

export default function Home() {
  return (
    <div className="min-h-screen bg-gray-50 text-gray-900 flex flex-col font-[family-name:var(--font-geist-sans)]">
      {/* Hero */}
      <main className="flex-grow">
        <section className="max-w-7xl mx-auto py-20 px-4 sm:px-6 lg:px-8 text-center">
          <h1 className="text-5xl font-extrabold text-gray-900 tracking-tight mb-6">
            Jual Tiket Tanpa Calo,{' '}
            <span className="text-blue-600">Tanpa Crash.</span>
          </h1>
          <p className="text-xl text-gray-600 max-w-2xl mx-auto mb-10">
            Platform tiket yang melindungi fans dari calo dan server crash.
            Dilengkapi dengan Redis Bitmask Locking dan Anti-Bot Engine.
          </p>
          <div className="flex flex-col sm:flex-row justify-center gap-4">
            <Button href="/auth/register" className="text-lg px-8 py-3">
              Mulai Sekarang
            </Button>
            <Button
              href="/auth/login"
              variant="outline"
              className="text-lg px-8 py-3"
            >
              Login
            </Button>
          </div>
        </section>

        {/* Features */}
        <section
          id="features"
          className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 pb-20"
        >
          <h2 className="text-3xl font-bold text-gray-900 text-center mb-4">
            Kenapa E-Tix Flash
          </h2>
          <p className="text-center text-gray-600 mb-12">
            Fondasi ticketing yang anti-calo, anti-oversell, dan ramah Cashflow.
          </p>
          <div className="grid gap-6 sm:grid-cols-2 lg:grid-cols-4">
            {features.map((feature) => (
              <Card key={feature.title} className="h-full">
                <h3 className="text-lg font-semibold text-gray-900 mb-2">
                  {feature.title}
                </h3>
                <p className="text-sm text-gray-600 leading-relaxed">
                  {feature.desc}
                </p>
              </Card>
            ))}
          </div>
        </section>

        {/* Pricing */}
        <section
          id="pricing"
          className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 pb-20"
        >
          <h2 className="text-3xl font-bold text-gray-900 text-center mb-4">
            Pricing untuk Event Organizer
          </h2>
          <p className="text-center text-gray-600 mb-12">
            Fee transparan, tanpa biaya tersembunyi di halaman checkout.
          </p>
          <div className="grid gap-6 lg:grid-cols-3 items-start">
            {pricingTiers.map((tier) => (
              <Card
                key={tier.name}
                className={
                  tier.highlighted
                    ? 'h-full border-blue-600 ring-1 ring-blue-600'
                    : 'h-full'
                }
              >
                <div className="flex items-baseline justify-between">
                  <h3 className="text-xl font-semibold text-gray-900">
                    {tier.name}
                  </h3>
                  <span className="text-2xl font-bold text-blue-600">
                    {tier.fee}
                  </span>
                </div>
                <p className="text-sm font-medium text-gray-700 mt-1">
                  {tier.price}
                </p>
                <p className="text-sm text-gray-600 mt-3 mb-4">{tier.desc}</p>
                <ul className="space-y-2 text-sm text-gray-700">
                  {tier.features.map((item) => (
                    <li key={item} className="flex items-start gap-2">
                      <span aria-hidden="true" className="text-blue-600">
                        ✓
                      </span>
                      <span>{item}</span>
                    </li>
                  ))}
                </ul>
                <div className="mt-6">
                  <Button
                    href="/auth/register"
                    variant={tier.highlighted ? 'primary' : 'outline'}
                    className="w-full"
                  >
                    Pilih {tier.name}
                  </Button>
                </div>
              </Card>
            ))}
          </div>
          <p className="text-center text-xs text-gray-500 mt-8">
            Harga resale pada tiket sekunder dibatasi maksimal 10% di atas harga
            asli, sesuai batas harga yang ditegakkan backend.
          </p>
        </section>
      </main>

      <footer className="py-6 text-center text-gray-500 text-sm">
        © 2026 E-Tix Flash. Portfolio project by M. Arif Aulia.{' '}
        <Link href="/auth/login" className="text-blue-600 hover:underline">
          Login
        </Link>
      </footer>
    </div>
  );
}
