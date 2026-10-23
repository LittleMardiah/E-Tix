import Link from "next/link";

export default function OfflinePage() {
  return (
    <main className="min-h-screen bg-gray-900 text-white flex items-center justify-center p-6">
      <div className="max-w-md text-center">
        <p className="text-sm uppercase tracking-widest text-gray-400">
          Mode Offline
        </p>
        <h1 className="mt-3 text-3xl font-bold">Koneksi terputus</h1>
        <p className="mt-4 text-gray-300">
          Halaman ini tersimpan di perangkat Anda. Sambungkan kembali internet
          untuk melanjutkan transaksi atau sinkronisasi scan gate.
        </p>
        <Link
          href="/"
          className="mt-8 inline-block rounded-lg bg-blue-600 px-6 py-3 font-semibold text-white hover:bg-blue-500"
        >
          Coba Lagi
        </Link>
      </div>
    </main>
  );
}
