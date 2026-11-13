'use client';

import Link from 'next/link';
import { usePathname, useRouter } from 'next/navigation';
import { useAuthStore } from '@/lib/stores/authStore';

const NAV_LINKS = [
  { href: '/customer/dashboard', label: 'Explore' },
  { href: '/customer/my-tickets', label: 'My Tickets' },
];

export default function CustomerLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const pathname = usePathname();
  const router = useRouter();
  const logout = useAuthStore((state) => state.logout);

  const handleLogout = () => {
    logout();
    router.push('/auth/login');
  };

  return (
    <div className="min-h-screen bg-[#FAFAFA]">
      <nav className="h-16 border-b border-[#E2E8F0] bg-white px-6 flex items-center justify-between sticky top-0 z-50">
        <Link
          href="/customer/dashboard"
          className="font-bold text-lg tracking-tight text-[#171717]"
        >
          E-Tix Flash
        </Link>

        <div className="flex items-center gap-6">
          {NAV_LINKS.map((link) => {
            const active =
              pathname === link.href || pathname.startsWith(`${link.href}/`);
            return (
              <Link
                key={link.href}
                href={link.href}
                aria-current={active ? 'page' : undefined}
                className={`text-sm transition-colors ${
                  active
                    ? 'font-medium text-[#171717]'
                    : 'text-[#4D4D4D] hover:text-[#171717]'
                }`}
              >
                {link.label}
              </Link>
            );
          })}
        </div>

        <button
          type="button"
          onClick={handleLogout}
          className="text-sm font-medium text-[#4D4D4D] px-3 py-2 rounded-lg hover:text-[#171717] hover:bg-[#FAFAFA] transition-colors"
        >
          Logout
        </button>
      </nav>

      <main className="min-h-screen bg-[#FAFAFA]">{children}</main>
    </div>
  );
}
