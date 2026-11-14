'use client';

import Link from 'next/link';
import { usePathname, useRouter } from 'next/navigation';
import { useAuthStore } from '@/lib/stores/authStore';
import { useRoleGuard } from '@/lib/hooks/useRoleGuard';

const NAV_ITEMS = [
  {
    href: '/eo/dashboard',
    label: 'Dashboard',
    isActive: (pathname: string) => pathname === '/eo/dashboard',
  },
  {
    href: '/eo/dashboard#events',
    label: 'My Events',
    isActive: (pathname: string) => pathname.startsWith('/eo/events'),
  },
];

export default function EOLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  const pathname = usePathname() ?? '';
  const router = useRouter();
  const logout = useAuthStore((state) => state.logout);
  // Admins may use the EO tools (backend allows admin on /eo write routes);
  // customers and gate staff are bounced to their own home.
  const checking = useRoleGuard('event_organizer', 'admin');

  const handleLogout = () => {
    logout();
    router.push('/auth/login');
  };

  if (checking) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-[#FAFAFA]">
        <p className="text-sm text-[#4D4D4D]">Memuat...</p>
      </div>
    );
  }

  return (
    <div className="min-h-screen bg-[#FAFAFA] text-[#171717] md:flex">
      <aside className="md:sticky md:top-0 md:h-screen md:w-60 md:shrink-0 border-b md:border-b-0 md:border-r border-[#E2E8F0] bg-white">
        <div className="px-5 py-5">
          <Link href="/eo/dashboard" className="text-lg font-bold tracking-tight">
            E-Tix Flash
          </Link>
          <p className="text-xs uppercase tracking-widest text-[#8F8F8F] mt-1">
            Organizer
          </p>
        </div>
        <nav className="flex gap-1 overflow-x-auto px-3 pb-3 md:flex-col md:pb-0">
          {NAV_ITEMS.map((item) => {
            const active = item.isActive(pathname);
            return (
              <Link
                key={item.href}
                href={item.href}
                aria-current={active ? 'page' : undefined}
                className={`shrink-0 rounded-lg px-3 py-2 text-sm transition-colors ${
                  active
                    ? 'bg-[#FAF2F2] font-medium text-[#171717]'
                    : 'text-[#4D4D4D] hover:bg-[#FAFAFA] hover:text-[#171717]'
                }`}
              >
                {item.label}
              </Link>
            );
          })}
        </nav>
        <div className="hidden md:block px-5 py-4 border-t border-[#E2E8F0] mt-4">
          <button
            type="button"
            onClick={handleLogout}
            className="text-sm text-[#4D4D4D] hover:text-[#171717] transition-colors"
          >
            Keluar
          </button>
        </div>
      </aside>
      <main className="min-w-0 flex-1 px-5 py-6 md:px-8 md:py-8">{children}</main>
    </div>
  );
}
