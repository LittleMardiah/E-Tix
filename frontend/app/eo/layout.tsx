import Link from 'next/link';

export default function EOLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  return (
    <div className="min-h-screen bg-gray-50 text-gray-900">
      <header className="bg-white border-b border-gray-200">
        <div className="max-w-6xl mx-auto px-4 py-4 flex items-center justify-between gap-4">
          <div>
            <Link href="/" className="text-xl font-bold text-gray-900">
              E-Tix Flash
            </Link>
            <p className="text-sm text-gray-500">Event Organizer Console</p>
          </div>
          <Link
            href="/customer/dashboard"
            className="text-sm font-medium text-blue-600 hover:underline"
          >
            Customer App
          </Link>
        </div>
      </header>
      <main>{children}</main>
    </div>
  );
}
