'use client';

import { useRouter } from 'next/navigation';
import { getAccessToken } from '@/lib/api/client';

/**
 * Public CTA: logged-in visitors continue to the seat/zone picker, anonymous
 * visitors are sent to the login screen and bounced back to this event.
 */
export function PublicBuyButton({ eventId }: { eventId: string }) {
  const router = useRouter();

  const handleClick = () => {
    const eventPath = `/events/${eventId}`;
    if (!getAccessToken()) {
      router.push(`/auth/login?redirect=${encodeURIComponent(eventPath)}`);
      return;
    }
    router.push(`/customer/events/${eventId}`);
  };

  return (
    <button
      type="button"
      onClick={handleClick}
      className="w-full sm:w-auto rounded-lg bg-[#D08080] px-6 py-3 text-sm font-semibold text-[#171717] transition-colors hover:bg-[#c06b6b]"
    >
      Beli Tiket
    </button>
  );
}
