'use client';

import Link from 'next/link';
import { usePathname, useRouter } from 'next/navigation';
import { useAuthStore } from '@/lib/stores/authStore';
import { useRoleGuard } from '@/lib/hooks/useRoleGuard';

const NAV_ITEMS = [
  {
    href: '/admin/dashboard',
    label: 'Dashboard',
    isActive: (pathname: string) => pathname === '/admin/dashboard',
  },
  {
    href: '/admin/dashboard#audit-log',
    label: 'Audit Log',
    isActive: (pathname: string) => pathname === '/admin/ledger',
  },
  {
    href: '/admin/users',
    label: 'All Users',
    isActive: (pathname: string) => pathname.startsWith('/admin/users'),
  },
  {
    href: '/admin/ledger',
    label: 'Ledger',
    isActive: (pathname: string) => pathname === '/admin/ledger',
  },
];

export default function AdminLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  const pathname = usePathname() ?? '';
  const router = useRouter();
  const logout = useAuthStore((state) => state.logout);
  // Admin-only: organizers used to be able to reach /admin/* (see backend
  // RBAC fix); they are now redirected to their own dashboard.
  const checking = useRoleGuard('admin');

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
          <Link href="/admin/dashboard" className="text-lg font-bold tracking-tight">
            E-Tix Flash
          </Link>
          <p className="text-xs uppercase tracking-widest text-[#8F8F8F] mt-1">
            Admin
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
