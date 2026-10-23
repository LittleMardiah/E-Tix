import Link from 'next/link';

const navItems = [
  { href: '/admin/ledger', label: 'Ledger' },
  { href: '/admin/users', label: 'Users' },
  { href: '/admin/blacklist', label: 'Blacklist' },
  { href: '/admin/force-release', label: 'Force Release' },
];

export default function AdminLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  return (
    <div className="min-h-screen bg-gray-50 text-gray-900">
      <header className="bg-white border-b border-gray-200">
        <div className="max-w-6xl mx-auto px-4 py-4 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <Link href="/" className="text-xl font-bold text-gray-900">
              E-Tix Flash
            </Link>
            <p className="text-sm text-gray-500">Admin Console</p>
          </div>
          <nav className="flex flex-wrap gap-4 text-sm font-medium">
            {navItems.map((item) => (
              <Link
                key={item.href}
                href={item.href}
                className="text-gray-600 hover:text-blue-600"
              >
                {item.label}
              </Link>
            ))}
          </nav>
        </div>
      </header>
      <main>{children}</main>
    </div>
  );
}
